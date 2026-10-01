package acceptance_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"testing"
)

func TestInvalidAmountsAreRejectedBeforeAnyTransfer(t *testing.T) {
	t.Parallel()
	demo := startDemonstration(t)

	for name, body := range map[string]string{
		"malformed text":         `{"amount": "twenty"}`,
		"quoted number":          `{"amount": "25"}`,
		"missing amount":         `{}`,
		"null amount":            `{"amount": null}`,
		"not JSON":               `amount=25`,
		"trailing data":          `{"amount": 25} junk`,
		"two objects":            `{"amount": 25}{"amount": 30}`,
		"trailing bracket":       `{"amount": 25}]`,
		"trailing brace":         `{"amount": 25}}`,
		"fractional":             `{"amount": 2.5}`,
		"fractional notation":    `{"amount": 25.0}`,
		"exponent notation":      `{"amount": 2.5e1}`,
		"zero":                   `{"amount": 0}`,
		"negative":               `{"amount": -5}`,
		"negative zero":          `{"amount": -0}`,
		"just above int64 range": `{"amount": 9223372036854775808}`,
		"far above int64 range":  `{"amount": 123456789012345678901234567890}`,
		"huge exponent":          `{"amount": 1e400}`,
	} {
		t.Run("API "+name, func(t *testing.T) {
			r := demo.post(t, "/api/transfers", "application/json", body)
			if r.status != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; body %q", r.status, http.StatusBadRequest, r.body)
			}
			var problem struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(r.body, &problem); err != nil || problem.Error == "" {
				t.Errorf("body %q does not explain the rejection", r.body)
			}
		})
	}

	for _, amount := range []string{"", "abc", "1.5", "0", "-3", "99999999999999999999"} {
		t.Run("page "+amount, func(t *testing.T) {
			r := demo.post(t, "/transfers", "application/x-www-form-urlencoded", url.Values{"amount": {amount}}.Encode())
			if r.status != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; Location %q", r.status, http.StatusBadRequest, r.location)
			}
			if !regexp.MustCompile(`id="amount-error"`).Match(r.body) {
				t.Errorf("page does not explain the rejection: %s", r.body)
			}
		})
	}

	if transfers := demo.transfers(t); len(transfers) != 0 {
		t.Errorf("transfers = %+v, want none", transfers)
	}
	demo.assertBalances(t, 100, 0)
}

func (d *demonstration) transfers(t *testing.T) []transfer {
	t.Helper()
	var list struct {
		Transfers []transfer `json:"transfers"`
	}
	body := d.get(t, "/api/transfers")
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode transfers %q: %v", body, err)
	}
	return list.Transfers
}
