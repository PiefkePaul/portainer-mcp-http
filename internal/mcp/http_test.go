package mcp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeHTTPPath(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		fallback    string
		want        string
		expectError bool
	}{
		{
			name:     "uses fallback when empty",
			input:    "",
			fallback: "/mcp",
			want:     "/mcp",
		},
		{
			name:     "adds leading slash",
			input:    "api/mcp",
			fallback: "/mcp",
			want:     "/api/mcp",
		},
		{
			name:     "removes trailing slash",
			input:    "/api/mcp/",
			fallback: "/mcp",
			want:     "/api/mcp",
		},
		{
			name:        "rejects spaces",
			input:       "/bad path",
			fallback:    "/mcp",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeHTTPPath(tt.input, tt.fallback)
			if tt.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewHTTPServer(t *testing.T) {
	portainerServer := newTestPortainerServer(t)

	httpServer, err := portainerServer.NewHTTPServer(HTTPServerConfig{
		Address:    ":8080",
		MCPPath:    "api/mcp/",
		HealthPath: "status",
	})
	require.NoError(t, err)
	require.NotNil(t, httpServer)

	req := httptest.NewRequest(http.MethodGet, "http://example.com/status", nil)
	rec := httptest.NewRecorder()
	httpServer.server.Handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"status":"ok","transport":"streamable-http","endpoint":"/api/mcp"}`, rec.Body.String())
}

func TestNewHTTPServerProtectsMCPPathWhenSecretConfigured(t *testing.T) {
	portainerServer := newTestPortainerServer(t)

	httpServer, err := portainerServer.NewHTTPServer(HTTPServerConfig{
		Address:    ":8080",
		MCPPath:    "/mcp",
		HealthPath: "/healthz",
		AuthSecret: "super-secret",
	})
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "http://example.com/mcp", nil)
	rec := httptest.NewRecorder()
	httpServer.server.Handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Equal(t, `Bearer realm="portainer-mcp"`, rec.Header().Get("WWW-Authenticate"))

	healthReq := httptest.NewRequest(http.MethodGet, "http://example.com/healthz", nil)
	healthRec := httptest.NewRecorder()
	httpServer.server.Handler.ServeHTTP(healthRec, healthReq)

	assert.Equal(t, http.StatusOK, healthRec.Code)
}

func TestNewHTTPServerRejectsCollidingPaths(t *testing.T) {
	portainerServer := newTestPortainerServer(t)

	httpServer, err := portainerServer.NewHTTPServer(HTTPServerConfig{
		Address:    ":8080",
		MCPPath:    "/mcp",
		HealthPath: "/mcp/",
	})

	require.Error(t, err)
	assert.Nil(t, httpServer)
	assert.Contains(t, err.Error(), "must be different")
}

func TestStrictPathHandlerRejectsNestedPaths(t *testing.T) {
	handler := strictPathHandler("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/mcp/nested", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestNewSecretAuthHandler(t *testing.T) {
	tests := []struct {
		name               string
		secret             string
		authorization      string
		xMCPSecret         string
		expectedStatusCode int
	}{
		{
			name:               "allows requests when no secret is configured",
			secret:             "",
			expectedStatusCode: http.StatusNoContent,
		},
		{
			name:               "rejects requests without secret",
			secret:             "super-secret",
			expectedStatusCode: http.StatusUnauthorized,
		},
		{
			name:               "allows bearer secret",
			secret:             "super-secret",
			authorization:      "Bearer super-secret",
			expectedStatusCode: http.StatusNoContent,
		},
		{
			name:               "allows bearer secret case insensitive",
			secret:             "super-secret",
			authorization:      "bearer super-secret",
			expectedStatusCode: http.StatusNoContent,
		},
		{
			name:               "allows x mcp secret",
			secret:             "super-secret",
			xMCPSecret:         "super-secret",
			expectedStatusCode: http.StatusNoContent,
		},
		{
			name:               "rejects wrong secret",
			secret:             "super-secret",
			xMCPSecret:         "wrong-secret",
			expectedStatusCode: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := newSecretAuthHandler(tt.secret, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))

			req := httptest.NewRequest(http.MethodPost, "http://example.com/mcp", nil)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			if tt.xMCPSecret != "" {
				req.Header.Set("X-MCP-Secret", tt.xMCPSecret)
			}

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.expectedStatusCode, rec.Code)
		})
	}
}

func newTestPortainerServer(t *testing.T) *PortainerMCPServer {
	t.Helper()

	mockClient := new(MockPortainerClient)
	server, err := NewPortainerMCPServer(
		"https://portainer.example.com",
		"test-token",
		"testdata/valid_tools.yaml",
		WithClient(mockClient),
		WithDisableVersionCheck(true),
	)
	require.NoError(t, err)

	return server
}
