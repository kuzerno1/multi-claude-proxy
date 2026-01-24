package cmd

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/kuzerno1/multi-claude-proxy/internal/account"
	"github.com/kuzerno1/multi-claude-proxy/internal/api"
	"github.com/kuzerno1/multi-claude-proxy/internal/config"
	"github.com/kuzerno1/multi-claude-proxy/internal/provider"
	"github.com/kuzerno1/multi-claude-proxy/internal/provider/antigravity"
	"github.com/kuzerno1/multi-claude-proxy/internal/provider/zai"
	"github.com/kuzerno1/multi-claude-proxy/internal/utils"
)

var (
	port           int
	fallback       bool
	softLimit      float64
	noSoftLimit    bool
)

// serveCmd represents the serve command
var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the proxy server",
	Long: `Start the multi-claude-proxy server that exposes an Anthropic-compatible API.

The server listens for requests and proxies them to the configured provider
(default: Antigravity/Google Cloud Code).

Soft Limits:
  By default, soft limits are enabled at 10%. When an account's quota drops
  below this threshold, it is marked as "soft-limited" and other accounts
  will be preferred. This prevents accounts from being drained to 0% quota,
  which triggers a 7-day reset timer.

Example:
  multi-claude-proxy serve
  multi-claude-proxy serve --port 8080 --debug
  multi-claude-proxy serve --fallback
  multi-claude-proxy serve --soft-limit 0.15    # Set soft limit to 15%
  multi-claude-proxy serve --no-soft-limit      # Disable soft limits`,
	RunE: runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)

	serveCmd.Flags().IntVarP(&port, "port", "p", config.DefaultPort, "Port to listen on")
	serveCmd.Flags().BoolVar(&fallback, "fallback", false, "Enable model fallback when quota exhausted")
	serveCmd.Flags().Float64Var(&softLimit, "soft-limit", config.DefaultSoftLimitThreshold, "Soft limit threshold (0.0-1.0, e.g., 0.10 for 10%)")
	serveCmd.Flags().BoolVar(&noSoftLimit, "no-soft-limit", false, "Disable soft limits entirely")
}

func runServe(cmd *cobra.Command, args []string) error {
	// Validate required environment variables
	if err := config.ValidateRequiredEnvVars(); err != nil {
		return fmt.Errorf("%v\n\nSet this variable to protect your proxy endpoints:\n  export PROXY_API_KEY=your-secret-key-here\n\nThen restart the server", err)
	}

	// Enable debug mode if flag is set or env var
	debug, _ := cmd.Flags().GetBool("debug")
	if !debug {
		debug = config.GetDebugEnabled()
	}
	if debug {
		utils.SetDebug(true)
	}

	// Determine soft limit settings
	// Check if flag was explicitly set, otherwise use env var
	if !cmd.Flags().Changed("soft-limit") {
		softLimit = config.GetSoftLimitThreshold()
	}
	softLimitEnabled := !noSoftLimit
	softLimitThreshold := softLimit
	if noSoftLimit {
		softLimitThreshold = 0.0
	}

	// Check if port flag was explicitly set, otherwise use env var
	if !cmd.Flags().Changed("port") {
		port = config.GetPort()
	}

	// Check if fallback flag was explicitly set, otherwise use env var
	if !cmd.Flags().Changed("fallback") {
		fallback = config.GetEnableFallback()
	}

	// Validate soft limit threshold
	if softLimitEnabled {
		if math.IsNaN(softLimitThreshold) || math.IsInf(softLimitThreshold, 0) {
			return fmt.Errorf("--soft-limit must be a finite number, got %v", softLimitThreshold)
		}
		if softLimitThreshold < 0.0 {
			return fmt.Errorf("--soft-limit must be >= 0.0, got %v", softLimitThreshold)
		}
		if softLimitThreshold > 1.0 {
			return fmt.Errorf("--soft-limit must be <= 1.0, got %v", softLimitThreshold)
		}
	}

	utils.Info("Starting multi-claude-proxy server...")
	utils.Info("Port: %d", port)
	utils.Info("Fallback: %v", fallback)
	utils.Info("Debug: %v", debug)
	if softLimitEnabled {
		utils.Info("Soft Limit: enabled at %.0f%%", softLimitThreshold*100)
	} else {
		utils.Info("Soft Limit: disabled")
	}

	ctx := context.Background()

	// Initialize account manager
	accountManager := account.NewManager("")
	if err := accountManager.Initialize(); err != nil {
		utils.Warn("[Server] Account manager initialization: %v", err)
	}

	// Configure soft limit settings
	accountManager.SetSoftLimitSettings(softLimitEnabled, softLimitThreshold)

	accounts := accountManager.GetAllAccounts()
	if len(accounts) > 0 {
		utils.Success("[Server] Loaded %d account(s)", len(accounts))
	}

	// Initialize provider registry
	registry := provider.NewRegistry()

	// Initialize Antigravity provider
	antigravityProvider := antigravity.NewProvider(accountManager, fallback)
	if err := antigravityProvider.Initialize(ctx); err != nil {
		utils.Warn("[Server] Antigravity provider init: %v", err)
	}
	if err := registry.Register(antigravityProvider); err != nil {
		return fmt.Errorf("failed to register antigravity provider: %w", err)
	}
	utils.Info("[Server] Antigravity provider registered with %d models", len(antigravityProvider.Models()))

	// Initialize Z.AI provider (only if Z.AI accounts exist)
	zaiAccountCount := accountManager.GetAccountCountByProvider("zai")
	if zaiAccountCount > 0 {
		zaiProvider := zai.NewProvider(accountManager)
		if err := zaiProvider.Initialize(ctx); err != nil {
			utils.Warn("[Server] Z.AI provider init: %v", err)
		} else {
			if len(zaiProvider.Models()) > 0 {
				if err := registry.Register(zaiProvider); err != nil {
					utils.Warn("[Server] Z.AI provider registration: %v", err)
				} else {
					utils.Info("[Server] Z.AI provider registered with %d models", len(zaiProvider.Models()))
				}
			} else {
				utils.Warn("[Server] Z.AI provider has no models, skipping registration")
			}
		}
	}

	utils.Info("[Server] Total registered models: %d", len(registry.AllModels()))

	// Create API server
	apiServer := api.NewServer(registry, accountManager)

	// Get configurable timeouts and bind address
	timeouts := config.GetServerTimeouts()
	bindAddr := config.GetBindAddress()

	server := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", bindAddr, port),
		Handler:      apiServer.Handler(),
		ReadTimeout:  timeouts.ReadTimeout,
		WriteTimeout: timeouts.WriteTimeout,
		IdleTimeout:  timeouts.IdleTimeout,
	}

	// Graceful shutdown
	done := make(chan bool)
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		utils.Info("Shutting down server...")

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			utils.Error("Server forced to shutdown: %v", err)
		}

		close(done)
	}()

	utils.Success("Server listening on http://localhost:%d", port)
	utils.Info("Press Ctrl+C to stop")

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}

	<-done
	utils.Success("Server stopped gracefully")
	return nil
}
