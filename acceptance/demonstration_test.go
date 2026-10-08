package acceptance_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dvinubius/saga-lab/internal/visitor"
)

type demonstration struct {
	baseURL string
	*visitorClient
}

type visitorClient struct {
	baseURL string
	client  *http.Client
}

func newVisitorClient(baseURL string) *visitorClient {
	jar, _ := cookiejar.New(nil)
	return &visitorClient{baseURL: baseURL, client: &http.Client{
		Timeout:       5 * time.Second,
		Jar:           jar,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func (d *visitorClient) visitorID(t *testing.T) string {
	t.Helper()
	base, _ := url.Parse(d.baseURL)
	for _, cookie := range d.client.Jar.Cookies(base) {
		if cookie.Name == visitor.CookieName {
			return cookie.Value
		}
	}
	t.Fatal("visitor cookie missing")
	return ""
}

func (d *demonstration) visitor(t *testing.T) *visitorClient {
	t.Helper()
	v := newVisitorClient(d.baseURL)
	v.get(t, "/api/balances")
	return v
}

func newDemonstration(baseURL string) demonstration {
	return demonstration{baseURL: baseURL, visitorClient: newVisitorClient(baseURL)}
}

type response struct {
	status     int
	location   string
	retryAfter string
	body       []byte
}

func (d *visitorClient) get(t *testing.T, path string) []byte {
	t.Helper()
	r := d.request(t, http.MethodGet, path, "", "")
	if r.status != http.StatusOK {
		t.Fatalf("GET %s: status %d, body %q", path, r.status, r.body)
	}
	return r.body
}

func (d *visitorClient) post(t *testing.T, path, contentType, body string) response {
	t.Helper()
	return d.request(t, http.MethodPost, path, contentType, body)
}

func (d *visitorClient) request(t *testing.T, method, path, contentType, body string) response {
	t.Helper()
	r, err := d.do(method, path, contentType, body)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (d *visitorClient) do(method, path, contentType, body string) (response, error) {
	request, err := http.NewRequest(method, d.baseURL+path, strings.NewReader(body))
	if err != nil {
		return response{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	r, err := d.client.Do(request)
	if err != nil {
		return response{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer r.Body.Close()
	var buffer bytes.Buffer
	if _, err := io.Copy(&buffer, r.Body); err != nil {
		return response{}, fmt.Errorf("read %s %s: %w", method, path, err)
	}
	return response{status: r.StatusCode, location: r.Header.Get("Location"), retryAfter: r.Header.Get("Retry-After"), body: buffer.Bytes()}, nil
}

func randomHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("generate name: %v", err)
	}
	return hex.EncodeToString(b)
}
