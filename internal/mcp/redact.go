package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"regexp"
	"strings"

	"github.com/PiefkePaul/portainer-mcp-http/pkg/portainer/models"
)

// Secret-Maskierung (Audit 2026-11-17, H3).
//
// listLocalStacks und dockerProxy (GET /containers/<id>/json) gaben bisher alle
// Umgebungswerte im Klartext aus. Jetzt wird jeder Wert durch einen Platzhalter
// mit kurzem Fingerabdruck ersetzt; so lässt sich "gleicher Wert?" weiter
// prüfen, ohne den Wert zu zeigen.
//
// Das ist Schutz gegen versehentliches Auslesen (Chat-Verläufe, Logs, Dateien),
// keine Sicherheitsgrenze: wer dockerProxy mit POST nutzen darf, kann per exec
// weiterhin alles lesen. Dafür gibt es den Read-only-Modus.
//
// PORTAINER_MCP_REVEAL_SECRETS=true schaltet die Maskierung ab.

const redactedPrefix = "<redacted:"

func revealSecrets() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("PORTAINER_MCP_REVEAL_SECRETS")))
	return v == "1" || v == "true" || v == "yes"
}

func redactValue(v string) string {
	if v == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(v))
	return redactedPrefix + "sha256:" + hex.EncodeToString(sum[:4]) + ">"
}

func isRedacted(v string) bool { return strings.HasPrefix(v, redactedPrefix) }

func redactLocalStacks(stacks []models.LocalStack) []models.LocalStack {
	if revealSecrets() {
		return stacks
	}
	out := make([]models.LocalStack, len(stacks))
	for i, s := range stacks {
		env := make([]models.LocalStackEnvVar, len(s.Env))
		for j, e := range s.Env {
			env[j] = models.LocalStackEnvVar{Name: e.Name, Value: redactValue(e.Value)}
		}
		s.Env = env
		out[i] = s
	}
	return out
}

// containsRedacted verhindert, dass ein maskierter Wert per updateLocalStack
// oder createLocalStack zurückgeschrieben wird und das echte Secret ersetzt.
func containsRedacted(env []models.LocalStackEnvVar) (string, bool) {
	for _, e := range env {
		if isRedacted(e.Value) {
			return e.Name, true
		}
	}
	return "", false
}

var containerInspectPath = regexp.MustCompile(`^/(v[0-9.]+/)?(containers|exec)/[^/]+/json/?$`)

// redactDockerResponse maskiert Config.Env in Container-Inspect-Antworten.
func redactDockerResponse(method, path string, body []byte) []byte {
	if revealSecrets() || !strings.EqualFold(method, "GET") || !containerInspectPath.MatchString(path) {
		return body
	}
	// UseNumber: große Ganzzahlen (z. B. NanoCpus) unverändert durchreichen.
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		return body
	}
	changed := false
	if cfg, ok := doc["Config"].(map[string]any); ok {
		if env, ok := cfg["Env"].([]any); ok {
			for i, item := range env {
				if s, ok := item.(string); ok {
					if k, v, found := strings.Cut(s, "="); found {
						env[i] = k + "=" + redactValue(v)
						changed = true
					}
				}
			}
		}
	}
	if !changed {
		return body
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return body
	}
	return bytes.TrimRight(buf.Bytes(), "\n")
}
