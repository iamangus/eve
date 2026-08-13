package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Listen             string
	AgentFoundryURL    string
	AgentFoundryKey    string
	AssistantAgentID   string
	TitleAgentID       string
	RouterAgentID      string
	EVEMCPURL          string
	ProactiveEnabled   bool
	WebPresenceTimeout time.Duration
	DataDir            string
	EmailPollInterval  time.Duration
	SMTPHost           string
	SMTPPort           int
	SMTPUsername       string
	SMTPPassword       string
	SMTPFrom           string
	MatrixHomeserver   string
	MatrixAccessToken  string
	MatrixUserID       string
	CalDAVURL          string
	CalDAVUsername     string
	CalDAVPassword     string
	CalDAVCalendarPath string
	CalReminderLead    time.Duration

	SMSToken   string
	VoiceToken string

	HistorianAgentID           string
	ContextBudgetTokens        int
	ContextTriggerFraction     float64
	ContextProtectedTailTokens int
	ContextChunkTokens         int
	ContextMemoryLimit         int
	ContextCurateInterval      time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Listen:             envOr("LISTEN", ":8090"),
		AgentFoundryURL:    envOr("AGENTFOUNDRY_URL", "http://localhost:3000"),
		AgentFoundryKey:    os.Getenv("AGENTFOUNDRY_API_KEY"),
		AssistantAgentID:   os.Getenv("ASSISTANT_AGENT_ID"),
		TitleAgentID:       os.Getenv("TITLE_AGENT_ID"),
		RouterAgentID:      os.Getenv("ROUTER_AGENT_ID"),
		EVEMCPURL:          envOr("EVEMCP_URL", "http://localhost:8090/mcp"),
		ProactiveEnabled:   boolEnv("PROACTIVE_ENABLED", true),
		WebPresenceTimeout: durationEnv("WEB_PRESENCE_TIMEOUT", 60*time.Second),
		DataDir:            envOr("DATA_DIR", "./data"),
		EmailPollInterval:  durationEnv("EMAIL_POLL_INTERVAL", 60*time.Second),
		SMTPHost:           os.Getenv("SMTP_HOST"),
		SMTPPort:           intEnv("SMTP_PORT", 587),
		SMTPUsername:       os.Getenv("SMTP_USERNAME"),
		SMTPPassword:       os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:           os.Getenv("SMTP_FROM"),
		MatrixHomeserver:  os.Getenv("MATRIX_HOMESERVER"),
		MatrixAccessToken: os.Getenv("MATRIX_ACCESS_TOKEN"),
		MatrixUserID:      os.Getenv("MATRIX_USER_ID"),
		CalDAVURL:          os.Getenv("CALDAV_URL"),
		CalDAVUsername:     os.Getenv("CALDAV_USERNAME"),
		CalDAVPassword:     os.Getenv("CALDAV_PASSWORD"),
		CalDAVCalendarPath: os.Getenv("CALDAV_CALENDAR_PATH"),
		CalReminderLead:    durationEnv("CAL_REMINDER_LEAD", 15*time.Minute),
		SMSToken:           os.Getenv("SMS_WEBHOOK_TOKEN"),
		VoiceToken:         os.Getenv("VOICE_WEBHOOK_TOKEN"),

		HistorianAgentID:           os.Getenv("HISTORIAN_AGENT_ID"),
		ContextBudgetTokens:        intEnv("CONTEXT_BUDGET_TOKENS", 64000),
		ContextTriggerFraction:     floatEnv("CONTEXT_TRIGGER_FRACTION", 0.5),
		ContextProtectedTailTokens: intEnv("CONTEXT_PROTECTED_TAIL_TOKENS", 12000),
		ContextChunkTokens:         intEnv("CONTEXT_CHUNK_TOKENS", 20000),
		ContextMemoryLimit:         intEnv("CONTEXT_MEMORY_LIMIT", 200),
		ContextCurateInterval:      durationEnv("CONTEXT_CURATE_INTERVAL", 24*time.Hour),
	}
	if cfg.AgentFoundryKey == "" {
		return Config{}, fmt.Errorf("AGENTFOUNDRY_API_KEY is required")
	}
	if cfg.AssistantAgentID == "" {
		return Config{}, fmt.Errorf("ASSISTANT_AGENT_ID is required")
	}
	return cfg, nil
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
