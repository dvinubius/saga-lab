package acceptance_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const (
	projectPrefixVariable = "SAGA_LAB_ACCEPTANCE_PREFIX"
	composeDeadline       = 3 * time.Minute
)

type demonstration struct {
	project string
	baseURL string
	client  *http.Client
}

func startDemonstration(t *testing.T) *demonstration {
	t.Helper()
	prefix := os.Getenv(projectPrefixVariable)
	if prefix == "" {
		t.Skipf("%s is not set; run scripts/test.sh", projectPrefixVariable)
	}
	project := prefix + randomHex(t)
	t.Logf("Compose project %s", project)

	t.Cleanup(func() {
		if t.Failed() {
			logs, _ := compose(project, "logs", "--no-color", "--tail=100")
			t.Logf("%s logs:\n%s", project, logs)
		}
		if out, err := compose(project, "down", "--volumes", "--remove-orphans"); err != nil {
			t.Errorf("stop %s: %v\n%s", project, err, out)
		}
	})
	if out, err := compose(project, "up", "--detach", "--no-build", "--wait"); err != nil {
		t.Fatalf("start %s: %v\n%s", project, err, out)
	}
	address, err := compose(project, "port", "transfer-service", "8080")
	if err != nil {
		t.Fatalf("find Transfer Service port: %v\n%s", err, address)
	}
	return &demonstration{
		project: project,
		baseURL: "http://" + strings.TrimSpace(string(address)),
		client: &http.Client{
			Timeout:       5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func compose(project string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), composeDeadline)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", append([]string{"compose", "--file", "../compose.yaml", "--project-name", project}, args...)...)
	command.WaitDelay = 10 * time.Second
	command.Env = append(os.Environ(),
		"POSTGRES_PORT=",
		"RABBITMQ_PORT=",
		"RABBITMQ_MANAGEMENT_PORT=",
		"TRANSFER_SERVICE_PORT=",
		"BANK_A_PORT=",
		"BANK_B_PORT=",
	)
	return command.CombinedOutput()
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
		t.Fatalf("generate project name: %v", err)
	}
	return hex.EncodeToString(b)
}
