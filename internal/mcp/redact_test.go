package mcp

import (
	"strings"
	"testing"

	"github.com/PiefkePaul/portainer-mcp-http/pkg/portainer/models"
)

func TestRedactLocalStacks(t *testing.T) {
	t.Setenv("PORTAINER_MCP_REVEAL_SECRETS", "")
	in := []models.LocalStack{{ID: 1, Env: []models.LocalStackEnvVar{{Name: "A", Value: "geheim"}, {Name: "B", Value: ""}}}}
	out := redactLocalStacks(in)
	if strings.Contains(out[0].Env[0].Value, "geheim") || !isRedacted(out[0].Env[0].Value) {
		t.Fatalf("not redacted: %q", out[0].Env[0].Value)
	}
	if out[0].Env[1].Value != "" {
		t.Fatalf("empty value should stay empty")
	}
	if in[0].Env[0].Value != "geheim" {
		t.Fatalf("input mutated")
	}
	if redactValue("x") != redactValue("x") || redactValue("x") == redactValue("y") {
		t.Fatalf("fingerprint not stable/distinct")
	}
}

func TestRevealSwitch(t *testing.T) {
	t.Setenv("PORTAINER_MCP_REVEAL_SECRETS", "true")
	in := []models.LocalStack{{Env: []models.LocalStackEnvVar{{Name: "A", Value: "geheim"}}}}
	if redactLocalStacks(in)[0].Env[0].Value != "geheim" {
		t.Fatal("reveal switch ignored")
	}
}

func TestContainsRedacted(t *testing.T) {
	if _, ok := containsRedacted([]models.LocalStackEnvVar{{Name: "A", Value: "klar"}}); ok {
		t.Fatal("false positive")
	}
	if n, ok := containsRedacted([]models.LocalStackEnvVar{{Name: "A", Value: redactValue("x")}}); !ok || n != "A" {
		t.Fatal("redacted value not detected")
	}
}

func TestRedactDockerResponse(t *testing.T) {
	t.Setenv("PORTAINER_MCP_REVEAL_SECRETS", "")
	body := []byte(`{"Id":"abc","Config":{"Env":["TOKEN=geheim","PATH=/usr/bin","EMPTY="]}}`)
	out := string(redactDockerResponse("GET", "/containers/abc/json", body))
	if strings.Contains(out, "geheim") || strings.Contains(out, "/usr/bin") {
		t.Fatalf("not redacted: %s", out)
	}
	if !strings.Contains(out, `"TOKEN=<redacted:sha256:`) || !strings.Contains(out, `"EMPTY="`) {
		t.Fatalf("unexpected: %s", out)
	}
	if string(redactDockerResponse("GET", "/v1.48/containers/abc/json", body)) == string(body) {
		t.Fatal("versioned path not redacted")
	}
	if string(redactDockerResponse("GET", "/containers/json", body)) != string(body) {
		t.Fatal("list path must stay unchanged")
	}
	if string(redactDockerResponse("POST", "/containers/abc/json", body)) != string(body) {
		t.Fatal("non-GET must stay unchanged")
	}
	big := string(redactDockerResponse("GET", "/containers/abc/json", []byte(`{"HostConfig":{"NanoCpus":9007199254740993},"Config":{"Env":["A=b"]}}`)))
	if !strings.Contains(big, "9007199254740993") {
		t.Fatalf("large integer changed: %s", big)
	}
	if string(redactDockerResponse("GET", "/containers/abc/json", []byte("kein json"))) != "kein json" {
		t.Fatal("non-json must pass")
	}
}
