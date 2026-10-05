package transferservice

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

type bankClient struct {
	name    string
	baseURL string
	http    *http.Client
}

func newBankClient(name, baseURL string) bankClient {
	return bankClient{name: name, baseURL: baseURL, http: &http.Client{Timeout: 3 * time.Second, Transport: otelhttp.NewTransport(http.DefaultTransport)}}
}

func (c bankClient) balance(ctx context.Context, visitorID string) (int64, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/accounts/"+url.PathEscape(visitorID), nil)
	if err != nil {
		return 0, fmt.Errorf("%s balance request: %w", c.name, err)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return 0, fmt.Errorf("%s balance request: %w", c.name, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%s balance request: %s", c.name, response.Status)
	}
	var account struct {
		Balance int64 `json:"balance"`
	}
	if err := json.NewDecoder(response.Body).Decode(&account); err != nil {
		return 0, fmt.Errorf("%s balance response: %w", c.name, err)
	}
	return account.Balance, nil
}

func (c bankClient) openAccount(ctx context.Context, visitorID string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+"/accounts/"+url.PathEscape(visitorID), nil)
	if err != nil {
		return fmt.Errorf("%s open account request: %w", c.name, err)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("%s open account request: %w", c.name, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("%s open account request: %s", c.name, response.Status)
	}
	return nil
}
