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
	response, err := c.accountRequest(ctx, http.MethodGet, visitorID, "balance", http.StatusOK)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	var account struct {
		Balance int64 `json:"balance"`
	}
	if err := json.NewDecoder(response.Body).Decode(&account); err != nil {
		return 0, fmt.Errorf("%s balance response: %w", c.name, err)
	}
	return account.Balance, nil
}

func (c bankClient) openAccount(ctx context.Context, visitorID string) error {
	response, err := c.accountRequest(ctx, http.MethodPut, visitorID, "open account", http.StatusNoContent)
	if err != nil {
		return err
	}
	return response.Body.Close()
}

func (c bankClient) accountRequest(ctx context.Context, method, visitorID, operation string, wantStatus int) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/accounts/"+url.PathEscape(visitorID), nil)
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
