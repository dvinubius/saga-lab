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

type recordingDelivery struct {
	resumed, paused []string
	resumeErr       error
}

func (d *recordingDelivery) Resume(id string) error {
	if d.resumeErr != nil {
		return d.resumeErr
	}
	d.resumed = append(d.resumed, id)
	return nil
}
func (d *recordingDelivery) Pause(id string) error { d.paused = append(d.paused, id); return nil }

func TestResumeDeliveryActsAtMostOnce(t *testing.T) {
	db := pgtest.NewDatabase(t)
	delivery := &recordingDelivery{}
	b := open(t, db, bank.Config{Role: bank.Destination, Delivery: delivery})
	command, err := messaging.New(context.Background(), "transfer", messaging.ResumeDelivery{TransferID: "transfer"}, "")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := bank.ResumeDelivery(b, command); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(delivery.resumed, []string{"transfer"}) {
		t.Fatalf("resumed = %v", delivery.resumed)
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
			delivery := &recordingDelivery{resumeErr: registrationErr}
			b := open(t, db, bank.Config{Role: bank.Destination, Delivery: delivery})
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
			delivery.resumeErr = nil
			if err := bank.ResumeDelivery(b, command); err != nil {
				t.Fatal(err)
			}
			assertObservations(t, db, messaging.DeliveryResumed)
		})
	}
}

func TestDedicatedCreditPausesAfterSuccessfulAcknowledgement(t *testing.T) {
	db := pgtest.NewDatabase(t)
	delivery := &recordingDelivery{}
	b := open(t, db, bank.Config{Role: bank.Destination, Delivery: delivery})
	command, err := messaging.New(context.Background(), "transfer", messaging.CreditFunds{TransferID: "transfer", VisitorID: "test-visitor", Amount: 25, Scenario: messaging.BankBUnavailable}, "")
	if err != nil {
		t.Fatal(err)
	}
	acked := false
	_, err = bank.DedicatedCredit(b, command, "transfer", func() error {
		if balance(t, b, "test-visitor") != 25 {
			t.Fatal("ack preceded credit")
		}
		if len(delivery.paused) != 0 {
			t.Fatal("pause preceded ack")
		}
		acked = true
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !acked || !reflect.DeepEqual(delivery.paused, []string{"transfer"}) {
		t.Fatalf("acked = %v, paused = %v", acked, delivery.paused)
	}
	assertObservations(t, db, messaging.DeliveryPaused)
}

func TestDedicatedCreditKeepsDeliveryOnWhenAcknowledgementFails(t *testing.T) {
	db := pgtest.NewDatabase(t)
	delivery := &recordingDelivery{}
	b := open(t, db, bank.Config{Role: bank.Destination, Delivery: delivery})
	command, err := messaging.New(context.Background(), "transfer", messaging.CreditFunds{TransferID: "transfer", VisitorID: "test-visitor", Amount: 25}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bank.DedicatedCredit(b, command, "transfer", func() error { return errors.New("ack failed") }); err == nil {
		t.Fatal("ack failure succeeded")
	}
	if len(delivery.paused) != 0 {
		t.Fatalf("paused = %v", delivery.paused)
	}
	if got := outbox(t, db); !reflect.DeepEqual(got, []string{messaging.FundsCreditedTopic}) {
		t.Fatalf("outbox = %v", got)
	}
}

func TestStaleDedicatedCreditCannotPauseAnotherTransfer(t *testing.T) {
	db := pgtest.NewDatabase(t)
	delivery := &recordingDelivery{}
	b := open(t, db, bank.Config{Role: bank.Destination, Delivery: delivery})
	command, err := messaging.New(context.Background(), "old-transfer", messaging.CreditFunds{TransferID: "old-transfer", VisitorID: "test-visitor", Amount: 25}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := bank.CreditFunds(b, command); err != nil {
		t.Fatal(err)
	}
	acked := false
	paused, err := bank.DedicatedCredit(b, command, "new-transfer", func() error { acked = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if paused || !acked || len(delivery.paused) != 0 {
		t.Fatalf("paused = %v, acked = %v, pause calls = %v", paused, acked, delivery.paused)
	}
	if got := balance(t, b, "test-visitor"); got != 25 {
		t.Fatalf("balance = %d", got)
	}
	assertObservations(t, db, messaging.DuplicateSuppressed)
}
