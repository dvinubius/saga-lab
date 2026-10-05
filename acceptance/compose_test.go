package acceptance_test

import (
	"context"
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

type composeDemonstration struct {
	demonstration
	project string
	env     []string
}

func startComposeDemonstration(t *testing.T) *composeDemonstration {
	t.Helper()
	return startProject(t, []string{"SAGA_LAB_OTLP_ENDPOINT="}, "transfer-service")
}

func startObservedDemonstration(t *testing.T) *composeDemonstration {
	t.Helper()
	return startProject(t, []string{"SAGA_LAB_OTLP_ENDPOINT=http://otel-collector:4318"})
}

func startProject(t *testing.T, env []string, services ...string) *composeDemonstration {
	t.Helper()
	if testing.Short() {
		t.Skip("starts a Compose project; skipped with -short")
	}
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
		if out, err := compose(project, nil, "down", "--timeout", "0", "--volumes", "--remove-orphans"); err != nil {
			t.Errorf("stop %s: %v\n%s", project, err, out)
		}
	})
	if out, err := compose(project, env, append([]string{"up", "--detach", "--no-build", "--wait"}, services...)...); err != nil {
		t.Fatalf("start %s: %v\n%s", project, err, out)
	}
	d := &composeDemonstration{demonstration: newDemonstration(""), project: project, env: env}
	d.reconnect(t)
	return d
}

func (d *composeDemonstration) reconnect(t *testing.T) {
	t.Helper()
	d.baseURL = "http://" + serviceAddress(t, d.project, "transfer-service", "8080")
	d.visitorClient.baseURL = d.baseURL
}

func (d *composeDemonstration) compose(t *testing.T, args ...string) {
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
		"SAGA_LAB_BANK_B_RESUME_WAIT=2.5s",
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
