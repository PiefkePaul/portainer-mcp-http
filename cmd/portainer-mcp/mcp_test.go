package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTransportFlags(t *testing.T) {
	tests := []struct {
		name          string
		transport     string
		certFile      string
		keyFile       string
		timeout       time.Duration
		expectError   bool
		errorContains string
	}{
		{
			name:      "stdio without tls is valid",
			transport: "stdio",
			timeout:   10 * time.Second,
		},
		{
			name:      "http with tls is valid",
			transport: "http",
			certFile:  "cert.pem",
			keyFile:   "key.pem",
			timeout:   10 * time.Second,
		},
		{
			name:          "rejects unsupported transport",
			transport:     "sse",
			timeout:       10 * time.Second,
			expectError:   true,
			errorContains: "transport must be one of",
		},
		{
			name:          "rejects tls with stdio",
			transport:     "stdio",
			certFile:      "cert.pem",
			keyFile:       "key.pem",
			timeout:       10 * time.Second,
			expectError:   true,
			errorContains: "TLS flags can only be used",
		},
		{
			name:          "rejects incomplete tls config",
			transport:     "http",
			certFile:      "cert.pem",
			timeout:       10 * time.Second,
			expectError:   true,
			errorContains: "must be set together",
		},
		{
			name:          "rejects non-positive shutdown timeout",
			transport:     "http",
			timeout:       0,
			expectError:   true,
			errorContains: "greater than zero",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTransportFlags(tt.transport, tt.certFile, tt.keyFile, tt.timeout)
			if tt.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
				return
			}

			require.NoError(t, err)
		})
	}
}

func TestNormalizeLogAddress(t *testing.T) {
	assert.Equal(t, "127.0.0.1:8080", normalizeLogAddress(":8080"))
	assert.Equal(t, "0.0.0.0:8080", normalizeLogAddress("0.0.0.0:8080"))
	assert.Equal(t, "127.0.0.1", normalizeLogAddress(""))
}
