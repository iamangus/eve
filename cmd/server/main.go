package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/iamangus/eve/frontend"
	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/chat"
	"github.com/iamangus/eve/internal/config"
	ctxmgr "github.com/iamangus/eve/internal/context"
	"github.com/iamangus/eve/internal/email"
	"github.com/iamangus/eve/internal/io"
	"github.com/iamangus/eve/internal/store"
	"github.com/iamangus/eve/internal/tasks"
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
	slog.Info("loaded config", "listen", cfg.Listen, "agentfoundry", cfg.AgentFoundryURL, "agent", cfg.AssistantAgentID)

	af, err := agentfoundry.NewClient(cfg.AgentFoundryURL, cfg.AgentFoundryKey)
	if err != nil {
		slog.Error("agentfoundry client", "error", err)
		os.Exit(1)
	}

	st, err := store.New(cfg.DataDir)
	if err != nil {
		slog.Error("store", "dir", cfg.DataDir, "error", err)
		os.Exit(1)
	}

	ctxMgr := ctxmgr.NewManager(st, af, ctxmgr.Config{
		AgentID:             cfg.HistorianAgentID,
		BudgetTokens:        cfg.ContextBudgetTokens,
		TriggerFraction:     cfg.ContextTriggerFraction,
		ProtectedTailTokens: cfg.ContextProtectedTailTokens,
		ChunkTokens:         cfg.ContextChunkTokens,
		MemoryLimit:         cfg.ContextMemoryLimit,
		CurateInterval:      cfg.ContextCurateInterval,
	})

	ioMgr, err := io.NewManager(st, af, io.Config{
		DataDir:          cfg.DataDir,
		ActivityTimeout:  cfg.WebPresenceTimeout,
		RouterAgentID:    cfg.RouterAgentID,
		AssistantAgentID: cfg.AssistantAgentID,
		ProactiveEnabled: cfg.ProactiveEnabled,
		EVEMCPURL:        cfg.EVEMCPURL,
	})
	if err != nil {
		slog.Error("io manager", "error", err)
		os.Exit(1)
	}
	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	ioMgr.EnableEmail(io.SMTPConfig{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		Username: cfg.SMTPUsername,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
	})
	matrixCfg := io.MatrixConfig{
		Homeserver:  cfg.MatrixHomeserver,
		AccessToken: cfg.MatrixAccessToken,
		UserID:      cfg.MatrixUserID,
	}
	var matrixE2EE *io.MatrixE2EE
	if matrixCfg.Enabled() {
		pickleKey := []byte(cfg.MatrixPickleKey)
		if len(pickleKey) == 0 {
			sum := sha256.Sum256([]byte(cfg.MatrixAccessToken))
			pickleKey = sum[:]
		}
		me, err := io.NewMatrixE2EE(rootCtx, matrixCfg, cfg.DataDir, pickleKey)
		if err != nil {
			slog.Error("matrix e2ee init failed; matrix channel disabled", "error", err)
			matrixCfg = io.MatrixConfig{}
		} else {
			matrixE2EE = me
			defer me.Close()
		}
	}
	ioMgr.EnableMatrix(matrixCfg, matrixE2EE)
	ioMgr.EnableCalendar(io.CalDAVConfig{
		URL:           cfg.CalDAVURL,
		Username:      cfg.CalDAVUsername,
		Password:      cfg.CalDAVPassword,
		CalendarPath:  cfg.CalDAVCalendarPath,
		ReminderLead:  cfg.CalReminderLead,
	})
	ioMgr.SetContext(ctxMgr)

	chatH := chat.NewHandler(st, af, cfg, ctxMgr, ioMgr)

	taskStore, err := tasks.NewStore(cfg.DataDir)
	if err != nil {
		slog.Error("task store", "dir", cfg.DataDir, "error", err)
		os.Exit(1)
	}
	taskMgr := tasks.NewManager(taskStore, af, tasks.Config{
		PollInterval:  2 * time.Second,
		DecisionAgent: cfg.AssistantAgentID,
		Cooldown:      30 * time.Second,
		Proactive:     cfg.ProactiveEnabled,
		Router:        ioMgr.Router.Notify,
		Presence:      ioMgr.PresenceSummary,
		IsBusy: func(convID string) bool {
			runID, err := st.ActiveRunForConversation(convID)
			return err == nil && runID != ""
		},
	})
	ioMgr.Tasks = taskMgr
	chatH.SetTasks(taskMgr)

	emailStore, err := store.NewEmailStore(cfg.DataDir)
	if err != nil {
		slog.Error("email store", "dir", cfg.DataDir, "error", err)
		os.Exit(1)
	}

	const triggerRunTimeout = 5 * time.Minute
	engine := trigger.NewEngine(emailStore, af, cfg.AssistantAgentID, triggerRunTimeout)
	triggerH := trigger.NewHandler(emailStore, af, engine)

	go chatH.Reconcile(rootCtx)
	go ctxMgr.Loop(rootCtx)
	taskMgr.Reconcile(rootCtx)
	go taskMgr.Run(rootCtx)
	go ioMgr.RunPresenceLoop(rootCtx)

	poller := email.NewPoller(emailStore, func(ctx context.Context, acct store.Account, msg store.EmailMessage) {
		sender := msg.From
		if fromAddr := extractAddress(sender); fromAddr != "" {
			sender = fromAddr
		}
		if ident := ioMgr.Ident.Resolve(io.ChannelEmail, sender); ident != nil && ident.Owner {
			_, err := ioMgr.Inbound(ctx, io.InboundMessage{
				Channel: io.ChannelEmail,
				Sender:  sender,
				Text:    msg.Body,
			})
			if err != nil {
				slog.Warn("email inbound", "from", sender, "error", err)
			}
			return
		}
		engine.HandleEmail(ctx, acct, msg)
	}, cfg.EmailPollInterval)
	poller.SetHealthHook(func(err error) {
		ioMgr.RecordPollHealth("email", err)
	})
	go poller.Run(rootCtx)

	if ioMgr.Matrix.Enabled() {
		mp := io.NewMatrixPoller(ioMgr.Matrix, ioMgr, matrixE2EE)
		go mp.Run(rootCtx)
	}

	if ioMgr.Cal != nil {
		cp := io.NewCalPoller(ioMgr.Cal, ioMgr, cfg.CalReminderLead)
		go cp.Run(rootCtx)
	}

	mux := http.NewServeMux()
	chatH.RegisterRoutes(mux)
	ctxMgr.RegisterRoutes(mux)
	triggerH.RegisterRoutes(mux)
	ioMgr.RegisterRoutes(mux)
	ioMgr.RegisterWebhooks(mux, io.WebhookConfig{
		SMSToken:   cfg.SMSToken,
		VoiceToken: cfg.VoiceToken,
	})

	mcpSrv := io.NewMCP(ioMgr)
	mux.Handle("/mcp", mcpSrv)

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

// extractAddress pulls the bare email address out of a RFC 5322 sender
// header, e.g. "Alice <alice@example.com>" -> "alice@example.com". Returns
// the input unchanged when there is no angle-bracket form.
func extractAddress(from string) string {
	open := strings.LastIndex(from, "<")
	close := strings.LastIndex(from, ">")
	if open >= 0 && close > open {
		return from[open+1 : close]
	}
	return from
}
