package acceptance_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"
)

func TestBrokerMetricsShowTheDeliveryWaitWithoutAConsumer(t *testing.T) {
	t.Parallel()
	demo := startObservedDemonstration(t)

	accepted := demo.submitTransfer(t, `{"amount":25,"scenario":"bank_b_unavailable"}`)
	completed := demo.awaitReadiness(t, accepted.TransferID, "completed")
	from := completed.RequestedAt

	prometheus := "http://" + serviceAddress(t, demo.project, "prometheus", "9090")
	deadline := time.Now().Add(30 * time.Second)
	for {
		ready := queryRange(t, prometheus, `rabbitmq_queue_messages_ready{queue="CreditFundsDedicated"}`, from, time.Now())
		consumers := map[time.Time]float64{}
		for _, s := range queryRange(t, prometheus, `rabbitmq_queue_consumers{queue="CreditFundsDedicated"}`, from, time.Now()) {
			consumers[s.at] = s.value
		}
		waited, delivered := false, false
		for _, s := range ready {
			if count, ok := consumers[s.at]; ok && s.value >= 1 && count == 0 {
				waited = true
			}
			if waited && s.value == 0 {
				delivered = true
			}
		}
		if waited && delivered {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("CreditFundsDedicated since transfer %s was requested: ready without a consumer = %t, then none ready = %t\nready: %+v\nconsumers: %+v", accepted.TransferID, waited, delivered, ready, consumers)
		}
		time.Sleep(time.Second)
	}
}

type sample struct {
	at    time.Time
	value float64
}

func queryRange(t *testing.T, prometheus, query string, from, to time.Time) []sample {
	t.Helper()
	parameters := url.Values{
		"query": {query},
		"start": {strconv.FormatInt(from.Unix(), 10)},
		"end":   {strconv.FormatInt(to.Unix()+1, 10)},
		"step":  {"1s"},
	}
	r, err := http.Get(prometheus + "/api/v1/query_range?" + parameters.Encode())
	if err != nil {
		t.Fatalf("query Prometheus: %v", err)
	}
	defer r.Body.Close()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("read Prometheus response: %v", err)
	}
	if r.StatusCode != http.StatusOK {
		t.Fatalf("query Prometheus %s: status %d, body %q", query, r.StatusCode, body)
	}
	var result struct {
		Data struct {
			Result []struct {
				Values [][2]any
			}
		}
	}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode Prometheus response %q: %v", body, err)
	}
	if len(result.Data.Result) > 1 {
		t.Fatalf("query %s returned %d series, want at most one: %s", query, len(result.Data.Result), body)
	}
	var samples []sample
	for _, s := range result.Data.Result {
		for _, v := range s.Values {
			at, _ := v[0].(float64)
			text, _ := v[1].(string)
			value, err := strconv.ParseFloat(text, 64)
			if err != nil {
				t.Fatalf("decode Prometheus sample %v: %v", v, err)
			}
			samples = append(samples, sample{at: time.Unix(int64(at), 0), value: value})
		}
	}
	return samples
}
