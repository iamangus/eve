package main

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/iamangus/eve/frontend"
	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/chat"
	"github.com/iamangus/eve/internal/config"
	"github.com/iamangus/eve/internal/email"
	"github.com/iamangus/eve/internal/store"
	"github.com/iamangus/eve/internal/trigger"
	"github.com/iamangus/eve/internal/web"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("config", "error", err)
		os.Exit(1)
	}
	slog.Info("loaded config", "listen", cfg.Listen, "agentfoundry", cfg.AgentFoundryURL, "agent", cfg.AssistantAgentID, "title_agent", cfg.TitleAgentID)

	af, err := agentfoundry.NewClient(cfg.AgentFoundryURL, cfg.AgentFoundryKey)
	if err != nil {
		slog.Error("agentfoundry client", "error", err)
		os.Exit(1)
	}

	chatH := chat.NewHandler(store.New(), af, cfg)

	emailStore, err := store.NewEmailStore(cfg.DataDir)
	if err != nil {
		slog.Error("email store", "dir", cfg.DataDir, "error", err)
		os.Exit(1)
	}

	const triggerRunTimeout = 5 * time.Minute
	engine := trigger.NewEngine(emailStore, af, cfg.AssistantAgentID, triggerRunTimeout)
	triggerH := trigger.NewHandler(emailStore, af, engine)

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	go chatH.Reconcile(rootCtx)

	poller := email.NewPoller(emailStore, func(ctx context.Context, acct store.Account, msg store.EmailMessage) {
		engine.HandleEmail(ctx, acct, msg)
	}, cfg.EmailPollInterval)
	go poller.Run(rootCtx)

	mux := http.NewServeMux()
	chatH.RegisterRoutes(mux)
	triggerH.RegisterRoutes(mux)

	distFS, err := fs.Sub(frontend.Dist, "dist")
	if err != nil {
		slog.Error("embed frontend", "error", err)
		os.Exit(1)
	}
	mux.HandleFunc("/", web.SPAHandler(distFS))

	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("eve starting", "addr", cfg.Listen)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-sigCtx.Done()
	slog.Info("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
	slog.Info("eve stopped")
}
