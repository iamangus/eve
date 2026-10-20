package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/iamangus/eve/frontend"
	"github.com/iamangus/eve/internal/agentfoundry"
	"github.com/iamangus/eve/internal/chat"
	"github.com/iamangus/eve/internal/config"
	ctxmgr "github.com/iamangus/eve/internal/context"
	"github.com/iamangus/eve/internal/durable"
	"github.com/iamangus/eve/internal/email"
	"github.com/iamangus/eve/internal/events"
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
	lock, err := durable.Lock(cfg.DataDir)
	if err != nil {
		slog.Error("data directory lock", "error", err)
		os.Exit(1)
	}
	defer lock.Close()

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
		DataDir:           cfg.DataDir,
		ActivityTimeout:   cfg.WebPresenceTimeout,
		RouterAgentID:     cfg.RouterAgentID,
		AssistantAgentID:  cfg.AssistantAgentID,
		ProactiveEnabled:  cfg.ProactiveEnabled,
		ProactiveCooldown: cfg.ProactiveCooldown,
		EVEMCPChatURL:     cfg.EVEMCPChatURL,
	})
	if err != nil {
		slog.Error("io manager", "error", err)
		os.Exit(1)
	}
	rootCtx, rootCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer rootCancel()
	var workers sync.WaitGroup
	start := func(fn func(context.Context)) {
		workers.Add(1)
		go func() { defer workers.Done(); fn(rootCtx) }()
	}

	ioMgr.EnableEmail(io.SMTPConfig{
		Host:      cfg.SMTPHost,
		Port:      cfg.SMTPPort,
		Username:  cfg.SMTPUsername,
		Password:  cfg.SMTPPassword,
		From:      cfg.SMTPFrom,
		Recipient: cfg.SMTPTo,
	})
	matrixCfg := io.MatrixConfig{
		Homeserver:  cfg.MatrixHomeserver,
		AccessToken: cfg.MatrixAccessToken,
		UserID:      cfg.MatrixUserID,
		OwnerRoom:   cfg.MatrixOwnerRoom,
	}
	var matrixE2EE *io.MatrixE2EE
	if matrixCfg.Enabled() {
		pickleKey := []byte(cfg.MatrixPickleKey)
		if len(pickleKey) == 0 {
			sum := sha256.Sum256([]byte(cfg.MatrixAccessToken))
			pickleKey = sum[:]
		}
		initCtx, cancelInit := context.WithTimeout(rootCtx, 45*time.Second)
		me, err := io.NewMatrixE2EE(initCtx, matrixCfg, cfg.DataDir, pickleKey)
		cancelInit()
		if err != nil {
			slog.Error("matrix e2ee init failed; matrix channel disabled", "error", err)
			matrixCfg = io.MatrixConfig{}
		} else {
			matrixE2EE = me
			matrixCfg.UserID = me.Client().UserID.String()
			defer me.Close()
		}
	}
	ioMgr.EnableMatrix(matrixCfg, matrixE2EE)
	if matrixE2EE != nil {
		// One-time bootstrap: make Eve's bot the account's trusted
		// cross-signing client. When the account has orphaned keys from the
		// provisioning browser session, the homeserver returns an
		// account-management approval URL that the owner must approve in a
		// browser. The URL is surfaced in the pod logs; the bot retries until
		// approved (Synapse's temporary 10-minute window) or times out. No
		// admin token and no permanent permission changes involved.
		start(func(ctx context.Context) {
			if err := matrixE2EE.EnsureCrossSigningSetup(rootCtx, func(url string) {
				slog.Warn("matrix cross-signing reset approval required", "url", url, "note", "open the URL, sign in as eve, and approve to make Eve the trusted client")
			}); err != nil {
				slog.Warn("matrix cross-signing setup", "error", err)
			}
		})
	}
	ioMgr.EnableCalendar(io.CalDAVConfig{
		URL:          cfg.CalDAVURL,
		Username:     cfg.CalDAVUsername,
		Password:     cfg.CalDAVPassword,
		CalendarPath: cfg.CalDAVCalendarPath,
		ReminderLead: cfg.CalReminderLead,
	})
	ioMgr.SetContext(ctxMgr)

	chatH := chat.NewHandler(st, af, ctxMgr, ioMgr)

	taskStore, err := tasks.NewStore(cfg.DataDir)
	if err != nil {
		slog.Error("task store", "dir", cfg.DataDir, "error", err)
		os.Exit(1)
	}
	// Automation-triggered runs (task transitions) get the full MCP surface
	// including send_message so Eve decides whether to contact the user.
	var fullMCPServers []agentfoundry.MCPServer
	if cfg.EVEMCPURL != "" {
		fullMCPServers = append(fullMCPServers, agentfoundry.MCPServer{
			Name:      "eve",
			URL:       cfg.EVEMCPURL,
			Transport: "streamable-http",
		})
	}
	taskMgr := tasks.NewManager(taskStore, af, tasks.Config{
		PollInterval:       cfg.TaskPollInterval,
		HistoryRetention:   cfg.TaskHistoryRetention,
		ConversationExists: func(id string) error { _, err := st.GetConversation(id); return err },
		DecisionAgent:      cfg.TaskDecisionAgent,
		Cooldown:           cfg.ProactiveCooldown,
		Proactive:          cfg.ProactiveEnabled,
		MCPServers:         fullMCPServers,
		Presence:           ioMgr.PresenceSummary,
		IsBusy: func(convID string) bool {
			runID, err := st.ActiveRunForConversation(convID)
			return err == nil && runID != ""
		},
	})
	ioMgr.Tasks = taskMgr
	chatH.SetTasks(taskMgr)

	eventQueue, err := events.NewQueue(cfg.DataDir)
	if err != nil {
		slog.Error("internal event queue", "dir", cfg.DataDir, "error", err)
		os.Exit(1)
	}
	eventH := events.NewHandler(eventQueue, st, af, cfg.BackendAgentID, cfg.AssistantAgentID, cfg.OpenDevWebhookToken, fullMCPServers, fullMCPServers)
	ioMgr.SetFrontendTrigger(eventH.EnqueueFrontend)

	emailStore, err := store.NewEmailStore(cfg.DataDir)
	if err != nil {
		slog.Error("email store", "dir", cfg.DataDir, "error", err)
		os.Exit(1)
	}

	const triggerRunTimeout = 5 * time.Minute
	engine := trigger.NewEngine(emailStore, af, cfg.AssistantAgentID, triggerRunTimeout)
	triggerH := trigger.NewHandler(emailStore, af, engine)

	start(chatH.Reconcile)
	start(ctxMgr.Loop)
	start(func(ctx context.Context) { taskMgr.Reconcile(ctx); taskMgr.Run(ctx) })
	start(ioMgr.RunPresenceLoop)
	start(ioMgr.RunOutbox)
	start(eventH.Run)
	start(engine.Run)
	start(func(ctx context.Context) {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			for _, queue := range []*events.Queue{eventQueue, ioMgr.Outbox, ioMgr.MatrixInbox} {
				if _, err := queue.ArchiveCompleted(time.Now().Add(-cfg.EventHistoryRetention)); err != nil {
					slog.Warn("archive completed events", "error", err)
				}
				if _, err := queue.ExpireCompleted(time.Now().Add(-cfg.EventDedupRetention)); err != nil {
					slog.Warn("expire completed event tombstones", "error", err)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})

	poller := email.NewPoller(emailStore, func(ctx context.Context, acct store.Account, msg store.EmailMessage) error {
		sender := msg.From
		if fromAddr := extractAddress(sender); fromAddr != "" {
			sender = fromAddr
		}
		if ident := ioMgr.Ident.Resolve(io.ChannelEmail, sender); ident != nil && ident.Owner && msg.AuthenticatedSender {
			sourceID := msg.MessageID
			if sourceID == "" {
				sourceID = fmt.Sprintf("%s:%d:%d", acct.ID, acct.UIDValidity, msg.UID)
			}
			_, err := ioMgr.Inbound(ctx, io.InboundMessage{
				Channel:  io.ChannelEmail,
				Sender:   sender,
				Text:     msg.Body,
				SourceID: sourceID,
			})
			if err != nil {
				slog.Warn("email inbound", "from", sender, "error", err)
			}
			return err
		}
		if ident := ioMgr.Ident.Resolve(io.ChannelEmail, sender); ident != nil && ident.Owner && !msg.AuthenticatedSender {
			slog.Warn("owner email not accepted as a conversation command; configure a trusted mail gateway", "account", acct.ID, "sender", sender)
		}
		return engine.HandleEmail(ctx, acct, msg)
	}, cfg.EmailPollInterval)
	poller.SetHealthHook(func(err error) {
		ioMgr.RecordPollHealth("email", err)
	})
	ioMgr.SetPollInterval("email", cfg.EmailPollInterval)
	start(poller.Run)

	if ioMgr.Matrix.Enabled() {
		ioMgr.SetPollInterval("matrix", 30*time.Second)
		mp := io.NewMatrixPoller(ioMgr.Matrix, ioMgr, matrixE2EE)
		start(mp.Run)
	}

	if ioMgr.Cal != nil {
		ioMgr.SetPollInterval("calendar", min(30*time.Second, max(time.Second, cfg.CalReminderLead/2)))
		cp := io.NewCalPoller(ioMgr.Cal, ioMgr, cfg.CalReminderLead)
		start(cp.Run)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if rootCtx.Err() != nil {
			http.Error(w, "shutting down", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	chatH.RegisterRoutes(mux)
	ctxMgr.RegisterRoutes(mux)
	triggerH.RegisterRoutes(mux)
	ioMgr.RegisterRoutes(mux)
	ioMgr.RegisterWebhooks(mux, io.WebhookConfig{
		SMSToken:   cfg.SMSToken,
		VoiceToken: cfg.VoiceToken,
	})
	eventH.RegisterRoutes(mux)

	mcpSrv := io.NewMCP(ioMgr)
	mux.Handle("/mcp", mcpSrv)
	chatMCP := io.NewChatMCP(ioMgr)
	mux.Handle("/mcp/chat", chatMCP)

	distFS, err := fs.Sub(frontend.Dist, "dist")
	if err != nil {
		slog.Error("embed frontend", "error", err)
		os.Exit(1)
	}
	mux.HandleFunc("/", web.SPAHandler(distFS))

	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           web.Harden(mux),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return rootCtx },
	}

	go func() {
		slog.Info("eve starting", "addr", cfg.Listen)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-rootCtx.Done()
	slog.Info("shutting down...")
	rootCancel()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
		_ = server.Close()
	}
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		slog.Warn("background workers exceeded shutdown deadline")
	}
	slog.Info("eve stopped")
}

// extractAddress pulls the bare email address out of a RFC 5322 sender
// header, e.g. "Alice <alice@example.com>" -> "alice@example.com". Returns
// the input unchanged when there is no angle-bracket form.
func extractAddress(from string) string {
	if addr, err := mail.ParseAddress(from); err == nil {
		return addr.Address
	}
	return from
}
