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
	env     []string
	baseURL string
	client  *http.Client
}

func startDemonstration(t *testing.T) *demonstration {
	t.Helper()
	return startProject(t, []string{"SAGA_LAB_OTLP_ENDPOINT="}, "transfer-service")
}

func startObservedDemonstration(t *testing.T) *demonstration {
	t.Helper()
	return startProject(t, []string{"SAGA_LAB_OTLP_ENDPOINT=http://otel-collector:4318"})
}

func startProject(t *testing.T, env []string, services ...string) *demonstration {
	t.Helper()
	prefix := os.Getenv(projectPrefixVariable)
	if prefix == "" {
		t.Skipf("%s is not set; run scripts/test.sh", projectPrefixVariable)
	}
	project := prefix + randomHex(t)
	t.Logf("Compose project %s", project)

	t.Cleanup(func() {
		if t.Failed() {
			logs, _ := compose(project, nil, "logs", "--no-color", "--tail=100")
			t.Logf("%s logs:\n%s", project, logs)
		}
		if out, err := compose(project, nil, "down", "--volumes", "--remove-orphans"); err != nil {
			t.Errorf("stop %s: %v\n%s", project, err, out)
		}
	})
	if out, err := compose(project, env, append([]string{"up", "--detach", "--no-build", "--wait"}, services...)...); err != nil {
		t.Fatalf("start %s: %v\n%s", project, err, out)
	}
	d := &demonstration{
		project: project,
		env:     env,
		client: &http.Client{
			Timeout:       5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
	d.reconnect(t)
	return d
}

func (d *demonstration) reconnect(t *testing.T) {
	t.Helper()
	d.baseURL = "http://" + serviceAddress(t, d.project, "transfer-service", "8080")
}

func (d *demonstration) compose(t *testing.T, args ...string) {
	t.Helper()
	if out, err := compose(d.project, d.env, args...); err != nil {
		t.Fatalf("docker compose %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func compose(project string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), composeDeadline)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", append([]string{"compose", "--file", "../compose.yaml", "--project-name", project}, args...)...)
	command.WaitDelay = 10 * time.Second
	command.Env = composeEnv(env)
	return command.CombinedOutput()
}

func composeEnv(env []string) []string {
	return append(append(os.Environ(),
		"POSTGRES_PORT=",
		"RABBITMQ_PORT=",
		"RABBITMQ_MANAGEMENT_PORT=",
		"TRANSFER_SERVICE_PORT=",
		"BANK_A_PORT=",
		"BANK_B_PORT=",
		"OTEL_COLLECTOR_HTTP_PORT=",
		"TEMPO_PORT=",
		"GRAFANA_PORT=",
	), env...)
}

func serviceAddress(t *testing.T, project, service, port string) string {
	t.Helper()
	address, err := compose(project, nil, "port", service, port)
	if err != nil {
		t.Fatalf("find %s port: %v\n%s", service, err, address)
	}
	return strings.TrimSpace(string(address))
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
