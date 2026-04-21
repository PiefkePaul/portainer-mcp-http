package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/PiefkePaul/portainer-mcp-http/internal/mcp"
	"github.com/PiefkePaul/portainer-mcp-http/internal/tooldef"
	"github.com/rs/zerolog/log"
)

const defaultToolsPath = "tools.yaml"
const defaultHTTPListenAddr = ":8080"

var (
	Version   string
	BuildDate string
	Commit    string
)

func main() {
	log.Info().
		Str("version", Version).
		Str("build-date", BuildDate).
		Str("commit", Commit).
		Msg("Portainer MCP server")

	serverFlag := flag.String("server", envOrDefault("PORTAINER_SERVER", ""), "The Portainer server URL")
	tokenFlag := flag.String("token", envOrDefault("PORTAINER_TOKEN", ""), "The authentication token for the Portainer server")
	toolsFlag := flag.String("tools", envOrDefault("PORTAINER_TOOLS_PATH", ""), "The path to the tools YAML file")
	readOnlyFlag := flag.Bool("read-only", envBoolOrDefault(false, "READ_ONLY", "PORTAINER_READ_ONLY"), "Run in read-only mode")
	disableVersionCheckFlag := flag.Bool("disable-version-check", envBoolOrDefault(false, "DISABLE_VERSION_CHECK", "PORTAINER_DISABLE_VERSION_CHECK"), "Disable Portainer server version check")
	transportFlag := flag.String("transport", envFirstOrDefault("stdio", "MCP_TRANSPORT"), "Transport to use: stdio or http")
	listenAddrFlag := flag.String("listen-addr", envFirstOrDefault(defaultHTTPListenAddr, "MCP_HTTP_LISTEN_ADDR", "MCP_LISTEN_ADDR"), "Address to listen on for HTTP transport")
	mcpPathFlag := flag.String("mcp-path", envFirstOrDefault("/mcp", "MCP_HTTP_PATH", "MCP_PATH"), "HTTP path used for the MCP endpoint")
	healthPathFlag := flag.String("health-path", envFirstOrDefault("/healthz", "MCP_HTTP_HEALTH_PATH", "MCP_HEALTH_PATH"), "HTTP path used for the health endpoint")
	tlsCertFileFlag := flag.String("tls-cert-file", envOrDefault("MCP_TLS_CERT_FILE", ""), "Path to the TLS certificate file for HTTPS")
	tlsKeyFileFlag := flag.String("tls-key-file", envOrDefault("MCP_TLS_KEY_FILE", ""), "Path to the TLS private key file for HTTPS")
	authSecretFlag := flag.String("auth-secret", envFirstOrDefault("", "MCP_HTTP_AUTH_SECRET", "MCP_AUTH_SECRET"), "Shared secret required for HTTP MCP requests; accepted via Authorization: Bearer <secret> or X-MCP-Secret")
	shutdownTimeoutFlag := flag.Duration("shutdown-timeout", 10*time.Second, "Graceful shutdown timeout for HTTP transport")

	flag.Parse()

	if *serverFlag == "" || *tokenFlag == "" {
		log.Fatal().Msg("Both -server and -token flags are required")
	}

	if err := validateTransportFlags(*transportFlag, *tlsCertFileFlag, *tlsKeyFileFlag, *shutdownTimeoutFlag); err != nil {
		log.Fatal().Err(err).Msg("invalid transport configuration")
	}

	toolsPath := *toolsFlag
	if toolsPath == "" {
		toolsPath = defaultToolsPath
	}

	// We first check if the tools.yaml file exists
	// We'll create it from the embedded version if it doesn't exist
	exists, err := tooldef.CreateToolsFileIfNotExists(toolsPath)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create tools.yaml file")
	}

	if exists {
		log.Info().Msg("using existing tools.yaml file")
	} else {
		log.Info().Msg("created tools.yaml file")
	}

	log.Info().
		Str("portainer-host", *serverFlag).
		Str("tools-path", toolsPath).
		Str("transport", *transportFlag).
		Bool("auth-enabled", strings.TrimSpace(*authSecretFlag) != "").
		Bool("read-only", *readOnlyFlag).
		Bool("disable-version-check", *disableVersionCheckFlag).
		Msg("starting MCP server")

	portainerServer, err := mcp.NewPortainerMCPServer(*serverFlag, *tokenFlag, toolsPath, mcp.WithReadOnly(*readOnlyFlag), mcp.WithDisableVersionCheck(*disableVersionCheckFlag))
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create server")
	}

	portainerServer.AddEnvironmentFeatures()
	portainerServer.AddEnvironmentGroupFeatures()
	portainerServer.AddTagFeatures()
	portainerServer.AddStackFeatures()
	portainerServer.AddLocalStackFeatures()
	portainerServer.AddSettingsFeatures()
	portainerServer.AddUserFeatures()
	portainerServer.AddTeamFeatures()
	portainerServer.AddAccessGroupFeatures()
	portainerServer.AddDockerProxyFeatures()
	portainerServer.AddKubernetesProxyFeatures()

	switch *transportFlag {
	case "stdio":
		err = portainerServer.Start()
	case "http":
		err = runHTTPTransport(portainerServer, *listenAddrFlag, *mcpPathFlag, *healthPathFlag, *tlsCertFileFlag, *tlsKeyFileFlag, *authSecretFlag, *shutdownTimeoutFlag)
	default:
		err = fmt.Errorf("unsupported transport %q", *transportFlag)
	}

	if err != nil {
		log.Fatal().Err(err).Msg("failed to start server")
	}
}

func validateTransportFlags(transport, certFile, keyFile string, shutdownTimeout time.Duration) error {
	switch transport {
	case "stdio", "http":
	default:
		return fmt.Errorf("transport must be one of: stdio, http")
	}

	if transport == "stdio" && (certFile != "" || keyFile != "") {
		return errors.New("TLS flags can only be used with -transport http")
	}

	if (certFile == "") != (keyFile == "") {
		return errors.New("both -tls-cert-file and -tls-key-file must be set together")
	}

	if shutdownTimeout <= 0 {
		return errors.New("shutdown-timeout must be greater than zero")
	}

	return nil
}

func runHTTPTransport(
	portainerServer *mcp.PortainerMCPServer,
	listenAddr string,
	mcpPath string,
	healthPath string,
	tlsCertFile string,
	tlsKeyFile string,
	authSecret string,
	shutdownTimeout time.Duration,
) error {
	httpServer, err := portainerServer.NewHTTPServer(mcp.HTTPServerConfig{
		Address:    listenAddr,
		MCPPath:    mcpPath,
		HealthPath: healthPath,
		AuthSecret: authSecret,
	})
	if err != nil {
		return err
	}

	scheme := "http"
	if tlsCertFile != "" {
		scheme = "https"
	}

	log.Info().
		Str("listen-addr", listenAddr).
		Str("mcp-endpoint", fmt.Sprintf("%s://%s%s", scheme, normalizeLogAddress(listenAddr), httpServer.MCPPath())).
		Str("health-endpoint", fmt.Sprintf("%s://%s%s", scheme, normalizeLogAddress(listenAddr), httpServer.HealthPath())).
		Bool("auth-enabled", strings.TrimSpace(authSecret) != "").
		Msg("HTTP MCP transport ready")

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		if tlsCertFile != "" {
			errCh <- httpServer.ListenAndServeTLS(tlsCertFile, tlsKeyFile)
			return
		}

		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err == nil || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		log.Info().Msg("shutting down HTTP MCP transport")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return err
	}

	if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}

func normalizeLogAddress(listenAddr string) string {
	if listenAddr == "" {
		return "127.0.0.1"
	}

	if listenAddr[0] == ':' {
		return "127.0.0.1" + listenAddr
	}

	return listenAddr
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}

func envFirstOrDefault(fallback string, keys ...string) string {
	for _, key := range keys {
		if value := os.Getenv(key); value != "" {
			return value
		}
	}

	return fallback
}

func envBoolOrDefault(fallback bool, keys ...string) bool {
	for _, key := range keys {
		value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
		switch value {
		case "":
			continue
		case "1", "true", "yes", "y", "on":
			return true
		case "0", "false", "no", "n", "off":
			return false
		default:
			return fallback
		}
	}

	return fallback
}
