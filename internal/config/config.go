package config

import (
	"fmt"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Listen                string
	AgentFoundryURL       string
	AgentFoundryKey       string
	AssistantAgentID      string
	BackendAgentID        string
	RouterAgentID         string
	EVEMCPURL             string
	EVEMCPChatURL         string
	ProactiveEnabled      bool
	TaskPollInterval      time.Duration
	TaskHistoryRetention  time.Duration
	EventHistoryRetention time.Duration
	EventDedupRetention   time.Duration
	TaskDecisionAgent     string
	ProactiveCooldown     time.Duration
	WebPresenceTimeout    time.Duration
	DataDir               string
	EmailPollInterval     time.Duration
	SMTPHost              string
	SMTPPort              int
	SMTPUsername          string
	SMTPPassword          string
	SMTPFrom              string
	SMTPTo                string
	MatrixHomeserver      string
	MatrixAccessToken     string
	MatrixUserID          string
	MatrixOwnerRoom       string
	MatrixPickleKey       string
	CalDAVURL             string
	CalDAVUsername        string
	CalDAVPassword        string
	CalDAVCalendarPath    string
	CalReminderLead       time.Duration

	SMSToken            string
	VoiceToken          string
	OpenDevWebhookToken string

	HistorianAgentID           string
	ContextBudgetTokens        int
	ContextTriggerFraction     float64
	ContextProtectedTailTokens int
	ContextChunkTokens         int
	ContextMemoryLimit         int
	ContextCurateInterval      time.Duration
}

func Load() (Config, error) {
	secrets := make(map[string]string)
	for _, name := range []string{"AGENTFOUNDRY_API_KEY", "SMTP_PASSWORD", "MATRIX_ACCESS_TOKEN", "MATRIX_PICKLE_KEY", "CALDAV_PASSWORD", "SMS_WEBHOOK_TOKEN", "VOICE_WEBHOOK_TOKEN", "OPENDEV_WEBHOOK_TOKEN"} {
		secrets[name] = os.Getenv(name)
		if path := os.Getenv(name + "_FILE"); path != "" {
			if secrets[name] != "" {
				return Config{}, fmt.Errorf("set either %s or %s_FILE, not both", name, name)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return Config{}, fmt.Errorf("read %s_FILE: %w", name, err)
			}
			if len(data) > 65536 {
				return Config{}, fmt.Errorf("%s_FILE exceeds 64 KiB", name)
			}
			secrets[name] = strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
		}
	}
	cfg := Config{
		Listen:                envOr("LISTEN", ":8090"),
		AgentFoundryURL:       envOr("AGENTFOUNDRY_URL", "http://localhost:3000"),
		AgentFoundryKey:       secrets["AGENTFOUNDRY_API_KEY"],
		AssistantAgentID:      os.Getenv("ASSISTANT_AGENT_ID"),
		BackendAgentID:        envOr("BACKEND_AGENT_ID", os.Getenv("ASSISTANT_AGENT_ID")),
		RouterAgentID:         os.Getenv("ROUTER_AGENT_ID"),
		EVEMCPURL:             envOr("EVEMCP_URL", "http://localhost:8090/mcp"),
		EVEMCPChatURL:         envOr("EVEMCP_CHAT_URL", ""),
		ProactiveEnabled:      boolEnv("PROACTIVE_ENABLED", true),
		TaskPollInterval:      durationEnv("TASK_POLL_INTERVAL", 2*time.Second),
		TaskHistoryRetention:  durationEnv("TASK_HISTORY_RETENTION", 0),
		EventHistoryRetention: durationEnv("EVENT_HISTORY_RETENTION", 720*time.Hour),
		EventDedupRetention:   durationEnv("EVENT_DEDUP_RETENTION", 2160*time.Hour),
		TaskDecisionAgent:     os.Getenv("TASK_DECISION_AGENT"),
		ProactiveCooldown:     durationEnv("PROACTIVE_COOLDOWN", 2*time.Minute),
		WebPresenceTimeout:    durationEnv("WEB_PRESENCE_TIMEOUT", 60*time.Second),
		DataDir:               envOr("DATA_DIR", "./data"),
		EmailPollInterval:     durationEnv("EMAIL_POLL_INTERVAL", 60*time.Second),
		SMTPHost:              os.Getenv("SMTP_HOST"),
		SMTPPort:              intEnv("SMTP_PORT", 587),
		SMTPUsername:          os.Getenv("SMTP_USERNAME"),
		SMTPPassword:          secrets["SMTP_PASSWORD"],
		SMTPFrom:              os.Getenv("SMTP_FROM"),
		SMTPTo:                os.Getenv("SMTP_TO"),
		MatrixHomeserver:      os.Getenv("MATRIX_HOMESERVER"),
		MatrixAccessToken:     secrets["MATRIX_ACCESS_TOKEN"],
		MatrixUserID:          os.Getenv("MATRIX_USER_ID"),
		MatrixOwnerRoom:       os.Getenv("MATRIX_OWNER_ROOM"),
		MatrixPickleKey:       secrets["MATRIX_PICKLE_KEY"],
		CalDAVURL:             os.Getenv("CALDAV_URL"),
		CalDAVUsername:        os.Getenv("CALDAV_USERNAME"),
		CalDAVPassword:        secrets["CALDAV_PASSWORD"],
		CalDAVCalendarPath:    os.Getenv("CALDAV_CALENDAR_PATH"),
		CalReminderLead:       durationEnv("CAL_REMINDER_LEAD", 15*time.Minute),
		SMSToken:              secrets["SMS_WEBHOOK_TOKEN"],
		VoiceToken:            secrets["VOICE_WEBHOOK_TOKEN"],
		OpenDevWebhookToken:   secrets["OPENDEV_WEBHOOK_TOKEN"],

		HistorianAgentID:           os.Getenv("HISTORIAN_AGENT_ID"),
		ContextBudgetTokens:        intEnv("CONTEXT_BUDGET_TOKENS", 64000),
		ContextTriggerFraction:     floatEnv("CONTEXT_TRIGGER_FRACTION", 0.5),
		ContextProtectedTailTokens: intEnv("CONTEXT_PROTECTED_TAIL_TOKENS", 12000),
		ContextChunkTokens:         intEnv("CONTEXT_CHUNK_TOKENS", 20000),
		ContextMemoryLimit:         intEnv("CONTEXT_MEMORY_LIMIT", 200),
		ContextCurateInterval:      durationEnv("CONTEXT_CURATE_INTERVAL", 24*time.Hour),
	}
	if cfg.EVEMCPChatURL == "" {
		cfg.EVEMCPChatURL = deriveChatURL(cfg.EVEMCPURL)
	}
	if cfg.AgentFoundryKey == "" {
		return Config{}, fmt.Errorf("AGENTFOUNDRY_API_KEY is required")
	}
	if cfg.AssistantAgentID == "" {
		return Config{}, fmt.Errorf("ASSISTANT_AGENT_ID is required")
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) validate() error {
	if value := os.Getenv("TASK_HISTORY_RETENTION"); value != "" {
		d, err := time.ParseDuration(value)
		if err != nil || d < 0 {
			return fmt.Errorf("TASK_HISTORY_RETENTION must be a nonnegative Go duration")
		}
	}
	for _, name := range []string{"WEB_PRESENCE_TIMEOUT", "EMAIL_POLL_INTERVAL", "CAL_REMINDER_LEAD", "CONTEXT_CURATE_INTERVAL", "TASK_POLL_INTERVAL", "PROACTIVE_COOLDOWN", "EVENT_HISTORY_RETENTION", "EVENT_DEDUP_RETENTION"} {
		if value := os.Getenv(name); value != "" {
			d, err := time.ParseDuration(value)
			if err != nil || d <= 0 {
				return fmt.Errorf("%s must be a positive Go duration", name)
			}
		}
	}
	for _, name := range []string{"SMTP_PORT", "CONTEXT_BUDGET_TOKENS", "CONTEXT_PROTECTED_TAIL_TOKENS", "CONTEXT_CHUNK_TOKENS", "CONTEXT_MEMORY_LIMIT"} {
		if value := os.Getenv(name); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil || n <= 0 {
				return fmt.Errorf("%s must be a positive integer", name)
			}
		}
	}
	if value := os.Getenv("PROACTIVE_ENABLED"); value != "" {
		if _, err := strconv.ParseBool(value); err != nil {
			return fmt.Errorf("PROACTIVE_ENABLED must be a boolean")
		}
	}
	if value := os.Getenv("CONTEXT_TRIGGER_FRACTION"); value != "" {
		f, err := strconv.ParseFloat(value, 64)
		if err != nil || !(f > 0 && f <= 1) {
			return fmt.Errorf("CONTEXT_TRIGGER_FRACTION must be in (0,1]")
		}
	}
	for name, value := range map[string]string{"AGENTFOUNDRY_URL": cfg.AgentFoundryURL, "EVEMCP_URL": cfg.EVEMCPURL, "EVEMCP_CHAT_URL": cfg.EVEMCPChatURL, "MATRIX_HOMESERVER": cfg.MatrixHomeserver, "CALDAV_URL": cfg.CalDAVURL} {
		if value == "" {
			continue
		}
		u, err := url.Parse(value)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
			return fmt.Errorf("%s must be an absolute HTTP(S) URL without credentials or fragment", name)
		}
	}
	if cfg.SMTPPort > 65535 {
		return fmt.Errorf("SMTP_PORT must be at most 65535")
	}
	if cfg.SMTPHost != "" {
		if _, err := mail.ParseAddress(cfg.SMTPFrom); err != nil {
			return fmt.Errorf("SMTP_FROM must be a valid address")
		}
		if cfg.SMTPTo != "" {
			if _, err := mail.ParseAddress(cfg.SMTPTo); err != nil {
				return fmt.Errorf("SMTP_TO must be a valid address")
			}
		}
		if (cfg.SMTPUsername == "") != (cfg.SMTPPassword == "") {
			return fmt.Errorf("SMTP_USERNAME and SMTP_PASSWORD must be configured together")
		}
	}
	if cfg.SMTPHost == "" && (cfg.SMTPFrom != "" || cfg.SMTPTo != "" || cfg.SMTPUsername != "" || cfg.SMTPPassword != "") {
		return fmt.Errorf("SMTP_HOST is required when SMTP settings are provided")
	}
	if cfg.CalDAVURL == "" && (cfg.CalDAVUsername != "" || cfg.CalDAVPassword != "" || cfg.CalDAVCalendarPath != "") {
		return fmt.Errorf("CALDAV_URL is required when CalDAV settings are provided")
	}
	if (cfg.CalDAVUsername == "") != (cfg.CalDAVPassword == "") {
		return fmt.Errorf("CALDAV_USERNAME and CALDAV_PASSWORD must be configured together")
	}
	if cfg.ContextChunkTokens > cfg.ContextBudgetTokens {
		return fmt.Errorf("CONTEXT_CHUNK_TOKENS must not exceed CONTEXT_BUDGET_TOKENS")
	}
	if (cfg.MatrixHomeserver == "") != (cfg.MatrixAccessToken == "") {
		return fmt.Errorf("MATRIX_HOMESERVER and MATRIX_ACCESS_TOKEN must be configured together")
	}
	if cfg.ContextProtectedTailTokens >= cfg.ContextBudgetTokens {
		return fmt.Errorf("CONTEXT_PROTECTED_TAIL_TOKENS must be smaller than CONTEXT_BUDGET_TOKENS")
	}
	if cfg.EventDedupRetention < cfg.EventHistoryRetention {
		return fmt.Errorf("EVENT_DEDUP_RETENTION must be at least EVENT_HISTORY_RETENTION")
	}
	if strings.TrimSpace(cfg.AssistantAgentID) == "" {
		return fmt.Errorf("ASSISTANT_AGENT_ID must not be blank")
	}
	return nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func boolEnv(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func durationEnv(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}

func intEnv(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func floatEnv(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f <= 1 {
			return f
		}
	}
	return def
}

// deriveChatURL derives the chat MCP surface URL from the full MCP URL by
// swapping the path to /mcp/chat (same host, same transport).
func deriveChatURL(mcpURL string) string {
	if mcpURL == "" {
		return ""
	}
	u, err := url.Parse(mcpURL)
	if err != nil {
		return mcpURL
	}
	u.Path = "/mcp/chat"
	return u.String()
}
