package mcp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"
)

const (
	defaultMCPPath    = "/mcp"
	defaultHealthPath = "/healthz"
)

// HTTPServerConfig contains the HTTP transport settings for the MCP server.
type HTTPServerConfig struct {
	Address    string
	MCPPath    string
	HealthPath string
	AuthSecret string
}

// HTTPServer wraps the net/http server and the MCP streamable HTTP handler so
// the process can shut both down cleanly.
type HTTPServer struct {
	server     *http.Server
	mcpHandler *mcpserver.StreamableHTTPServer
	mcpPath    string
	healthPath string
}

type healthResponse struct {
	Status    string `json:"status"`
	Transport string `json:"transport"`
	Endpoint  string `json:"endpoint"`
}

// NewHTTPServer creates an HTTP server that exposes the MCP server via
// streamable HTTP and also publishes a lightweight health endpoint.
func (s *PortainerMCPServer) NewHTTPServer(cfg HTTPServerConfig) (*HTTPServer, error) {
	mcpPath, err := normalizeHTTPPath(cfg.MCPPath, defaultMCPPath)
	if err != nil {
		return nil, fmt.Errorf("invalid MCP path: %w", err)
	}

	healthPath, err := normalizeHTTPPath(cfg.HealthPath, defaultHealthPath)
	if err != nil {
		return nil, fmt.Errorf("invalid health path: %w", err)
	}

	if mcpPath == healthPath {
		return nil, errors.New("MCP path and health path must be different")
	}

	handler := s.StreamableHTTPHandler()
	protectedHandler := newSecretAuthHandler(cfg.AuthSecret, handler)
	mux := http.NewServeMux()
	mux.Handle(mcpPath, strictPathHandler(mcpPath, protectedHandler))
	if mcpPath != "/" {
		mux.Handle(mcpPath+"/", strictPathHandler(mcpPath, protectedHandler))
	}

	healthHandler := newHealthHandler(mcpPath)
	mux.Handle(healthPath, strictPathHandler(healthPath, healthHandler))
	if healthPath != "/" {
		mux.Handle(healthPath+"/", strictPathHandler(healthPath, healthHandler))
	}

	httpServer := &http.Server{
		Addr:              cfg.Address,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	return &HTTPServer{
		server:     httpServer,
		mcpHandler: handler,
		mcpPath:    mcpPath,
		healthPath: healthPath,
	}, nil
}

// MCPPath returns the public path of the streamable HTTP endpoint.
func (s *HTTPServer) MCPPath() string {
	return s.mcpPath
}

// HealthPath returns the health endpoint path.
func (s *HTTPServer) HealthPath() string {
	return s.healthPath
}

// ListenAndServe starts the HTTP server.
func (s *HTTPServer) ListenAndServe() error {
	return s.server.ListenAndServe()
}

// ListenAndServeTLS starts the HTTPS server with the provided certificate pair.
func (s *HTTPServer) ListenAndServeTLS(certFile, keyFile string) error {
	return s.server.ListenAndServeTLS(certFile, keyFile)
}

// Shutdown gracefully stops the HTTP transport and the underlying MCP handler.
func (s *HTTPServer) Shutdown(ctx context.Context) error {
	var shutdownErr error

	if err := s.mcpHandler.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		shutdownErr = errors.Join(shutdownErr, err)
	}

	if err := s.server.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		shutdownErr = errors.Join(shutdownErr, err)
	}

	return shutdownErr
}

func normalizeHTTPPath(path, fallback string) (string, error) {
	normalized := strings.TrimSpace(path)
	if normalized == "" {
		normalized = fallback
	}

	if !strings.HasPrefix(normalized, "/") {
		normalized = "/" + normalized
	}

	if normalized != "/" {
		normalized = strings.TrimRight(normalized, "/")
	}

	if strings.Contains(normalized, " ") {
		return "", errors.New("path must not contain spaces")
	}

	return normalized, nil
}

func strictPathHandler(path string, next http.Handler) http.Handler {
	trailingSlashPath := path
	if path != "/" {
		trailingSlashPath = path + "/"
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path && r.URL.Path != trailingSlashPath {
			http.NotFound(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func newHealthHandler(mcpPath string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		response := healthResponse{
			Status:    "ok",
			Transport: "streamable-http",
			Endpoint:  mcpPath,
		}

		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}

		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, "failed to write health response", http.StatusInternalServerError)
		}
	})
}

func newSecretAuthHandler(secret string, next http.Handler) http.Handler {
	trimmedSecret := strings.TrimSpace(secret)
	if trimmedSecret == "" {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providedSecret := extractRequestSecret(r)
		if !secretsMatch(trimmedSecret, providedSecret) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="portainer-mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func extractRequestSecret(r *http.Request) string {
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if authorization != "" {
		scheme, token, found := strings.Cut(authorization, " ")
		if found && strings.EqualFold(scheme, "Bearer") {
			return strings.TrimSpace(token)
		}
	}

	return strings.TrimSpace(r.Header.Get("X-MCP-Secret"))
}

func secretsMatch(expectedSecret, providedSecret string) bool {
	if expectedSecret == "" || providedSecret == "" {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(expectedSecret), []byte(providedSecret)) == 1
}
