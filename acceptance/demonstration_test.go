package acceptance_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type demonstration struct {
	baseURL string
	client  *http.Client
}

func newDemonstration(baseURL string) demonstration {
	return demonstration{
		baseURL: baseURL,
		client: &http.Client{
			Timeout:       5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

type response struct {
	status   int
	location string
	body     []byte
}

func (d *demonstration) get(t *testing.T, path string) []byte {
	t.Helper()
	r := d.request(t, http.MethodGet, path, "", "")
	if r.status != http.StatusOK {
		t.Fatalf("GET %s: status %d, body %q", path, r.status, r.body)
	}
	return r.body
}

func (d *demonstration) post(t *testing.T, path, contentType, body string) response {
	t.Helper()
	return d.request(t, http.MethodPost, path, contentType, body)
}

func (d *demonstration) request(t *testing.T, method, path, contentType, body string) response {
	t.Helper()
	r, err := d.do(method, path, contentType, body)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (d *demonstration) do(method, path, contentType, body string) (response, error) {
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
	return response{status: r.StatusCode, location: r.Header.Get("Location"), body: buffer.Bytes()}, nil
}

func randomHex(t *testing.T) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("generate name: %v", err)
	}
	return hex.EncodeToString(b)
}
