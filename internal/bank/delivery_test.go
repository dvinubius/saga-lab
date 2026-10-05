package bank_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/dvinubius/saga-lab/internal/bank"
	"github.com/dvinubius/saga-lab/internal/messaging"
	"github.com/dvinubius/saga-lab/internal/postgres/pgtest"
	"github.com/jackc/pgx/v5"
)

type recordingConsumer struct {
	resumed, paused     []string
	resumeErr, pauseErr error
}

func (c *recordingConsumer) Resume(id string) error {
	if c.resumeErr != nil {
		return c.resumeErr
	}
	c.resumed = append(c.resumed, id)
	return nil
}

func (c *recordingConsumer) Pause(id string) error {
	if c.pauseErr != nil {
		return c.pauseErr
	}
	c.paused = append(c.paused, id)
	return nil
}

type acknowledgement struct {
	ack    func() error
	acked  bool
	nacked bool
}

func (a *acknowledgement) Ack(bool) error {
	if a.ack != nil {
		if err := a.ack(); err != nil {
			return err
		}
	}
	a.acked = true
	return nil
}

func (a *acknowledgement) Nack(bool, bool) error { a.nacked = true; return nil }

func TestResumeDeliveryActsAtMostOnce(t *testing.T) {
	db := pgtest.NewDatabase(t)
	consumer := &recordingConsumer{}
	b := open(t, db, bank.Config{Role: bank.Destination, DedicatedConsumer: consumer})
	command, err := messaging.New(context.Background(), "transfer", messaging.ResumeDelivery{TransferID: "transfer"}, "")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := bank.ResumeDelivery(b, command); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(consumer.resumed, []string{"transfer"}) {
		t.Fatalf("resumed = %v", consumer.resumed)
	}
	assertObservations(t, db, messaging.DeliveryResumed, messaging.DuplicateSuppressed)
	if got := inbox(t, db); got != 1 {
		t.Fatalf("inbox = %d, want 1", got)
	}
}

func TestFailedResumeCanBeRedelivered(t *testing.T) {
	for name, registrationErr := range map[string]error{"registration refused": errors.New("registration refused"), "no rows": pgx.ErrNoRows} {
		t.Run(name, func(t *testing.T) {
			db := pgtest.NewDatabase(t)
			consumer := &recordingConsumer{resumeErr: registrationErr}
			b := open(t, db, bank.Config{Role: bank.Destination, DedicatedConsumer: consumer})
			command, err := messaging.New(context.Background(), "transfer", messaging.ResumeDelivery{TransferID: "transfer"}, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := bank.ResumeDelivery(b, command); err == nil {
				t.Fatal("failing resume succeeded")
			}
			if got := inbox(t, db); got != 0 {
				t.Fatalf("inbox = %d, want none", got)
			}
			if got := outbox(t, db); len(got) != 0 {
				t.Fatalf("outbox = %v, want none", got)
			}
			consumer.resumeErr = nil
			if err := bank.ResumeDelivery(b, command); err != nil {
				t.Fatal(err)
			}
			assertObservations(t, db, messaging.DeliveryResumed)
		})
	}
}

func TestDedicatedCreditPausesAfterSuccessfulAcknowledgement(t *testing.T) {
	db := pgtest.NewDatabase(t)
	consumer := &recordingConsumer{}
	b := open(t, db, bank.Config{Role: bank.Destination, DedicatedConsumer: consumer})
	command, err := messaging.New(context.Background(), "transfer", messaging.CreditFunds{TransferID: "transfer", VisitorID: "test-visitor", Amount: 25, Scenario: messaging.BankBUnavailable}, "")
	if err != nil {
		t.Fatal(err)
	}
	delivery := &acknowledgement{ack: func() error {
		if balance(t, b, "test-visitor") != 25 {
			t.Fatal("ack preceded credit")
		}
		if len(consumer.paused) != 0 {
			t.Fatal("pause preceded ack")
		}
		return nil
	}}
	stop, err := bank.DedicatedCredit(b, command, "transfer", delivery)
	if err != nil {
		t.Fatal(err)
	}
	if !stop || !delivery.acked || !reflect.DeepEqual(consumer.paused, []string{"transfer"}) {
		t.Fatalf("stop = %v, acked = %v, paused = %v", stop, delivery.acked, consumer.paused)
	}
	assertObservations(t, db, messaging.DeliveryPaused)
}

func TestDedicatedCreditKeepsDeliveryOnWhenAcknowledgementFails(t *testing.T) {
	db := pgtest.NewDatabase(t)
	consumer := &recordingConsumer{}
	b := open(t, db, bank.Config{Role: bank.Destination, DedicatedConsumer: consumer})
	command, err := messaging.New(context.Background(), "transfer", messaging.CreditFunds{TransferID: "transfer", VisitorID: "test-visitor", Amount: 25}, "")
	if err != nil {
		t.Fatal(err)
	}
	stop, err := bank.DedicatedCredit(b, command, "transfer", &acknowledgement{ack: func() error { return errors.New("ack failed") }})
	if err == nil || !stop {
		t.Fatalf("ack failure: stop = %v, err = %v", stop, err)
	}
	if len(consumer.paused) != 0 {
		t.Fatalf("paused = %v", consumer.paused)
	}
	if got := outbox(t, db); !reflect.DeepEqual(got, []string{messaging.FundsCreditedTopic}) {
		t.Fatalf("outbox = %v", got)
	}
}

func TestStaleDedicatedCreditCannotPauseAnotherTransfer(t *testing.T) {
	db := pgtest.NewDatabase(t)
	consumer := &recordingConsumer{}
	b := open(t, db, bank.Config{Role: bank.Destination, DedicatedConsumer: consumer})
	command, err := messaging.New(context.Background(), "old-transfer", messaging.CreditFunds{TransferID: "old-transfer", VisitorID: "test-visitor", Amount: 25}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := bank.CreditFunds(b, command); err != nil {
		t.Fatal(err)
	}
	delivery := &acknowledgement{}
	stop, err := bank.DedicatedCredit(b, command, "new-transfer", delivery)
	if err != nil {
		t.Fatal(err)
	}
	if stop || !delivery.acked || len(consumer.paused) != 0 {
		t.Fatalf("stop = %v, acked = %v, pause calls = %v", stop, delivery.acked, consumer.paused)
	}
	if got := balance(t, b, "test-visitor"); got != 25 {
		t.Fatalf("balance = %d", got)
	}
	assertObservations(t, db, messaging.DuplicateSuppressed)
}

func TestDedicatedCreditStopsWithoutPausedObservationWhenPauseFails(t *testing.T) {
	db := pgtest.NewDatabase(t)
	consumer := &recordingConsumer{pauseErr: errors.New("cancel failed")}
	b := open(t, db, bank.Config{Role: bank.Destination, DedicatedConsumer: consumer})
	command, err := messaging.New(context.Background(), "transfer", messaging.CreditFunds{TransferID: "transfer", VisitorID: "test-visitor", Amount: 25}, "")
	if err != nil {
		t.Fatal(err)
	}
	delivery := &acknowledgement{}
	stop, err := bank.DedicatedCredit(b, command, "transfer", delivery)
	if err == nil || !stop || !delivery.acked {
		t.Fatalf("stop = %v, acked = %v, err = %v", stop, delivery.acked, err)
	}
	if got := outbox(t, db); !reflect.DeepEqual(got, []string{messaging.FundsCreditedTopic}) {
		t.Fatalf("outbox = %v", got)
	}
}
