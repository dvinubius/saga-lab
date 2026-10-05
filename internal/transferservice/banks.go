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
	return c.accountBalance(ctx, http.MethodGet, accountPath(visitorID), "balance")
}

func (c bankClient) topUp(ctx context.Context, visitorID string) (int64, error) {
	return c.accountBalance(ctx, http.MethodPost, accountPath(visitorID)+"/top-ups", "top-up")
}

func (c bankClient) accountBalance(ctx context.Context, method, path, operation string) (int64, error) {
	response, err := c.accountRequest(ctx, method, path, operation, http.StatusOK)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	var account struct {
		Balance int64 `json:"balance"`
	}
	if err := json.NewDecoder(response.Body).Decode(&account); err != nil {
		return 0, fmt.Errorf("%s %s response: %w", c.name, operation, err)
	}
	return account.Balance, nil
}

func (c bankClient) openAccount(ctx context.Context, visitorID string) error {
	response, err := c.accountRequest(ctx, http.MethodPut, accountPath(visitorID), "open account", http.StatusNoContent)
	if err != nil {
		return err
	}
	return response.Body.Close()
}

func accountPath(visitorID string) string {
	return "/accounts/" + url.PathEscape(visitorID)
}

func (c bankClient) accountRequest(ctx context.Context, method, path, operation string, wantStatus int) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("%s %s request: %w", c.name, operation, err)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s %s request: %w", c.name, operation, err)
	}
	if response.StatusCode != wantStatus {
		response.Body.Close()
		return nil, fmt.Errorf("%s %s request: %s", c.name, operation, response.Status)
	}
	return response, nil
}
