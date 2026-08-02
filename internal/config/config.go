package config

import (
	"fmt"
	"os"
	"time"
)

type Config struct {
	Listen            string
	AgentFoundryURL   string
	AgentFoundryKey   string
	AssistantAgentID  string
	TitleAgentID      string
	DataDir           string
	EmailPollInterval time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Listen:            envOr("LISTEN", ":8090"),
		AgentFoundryURL:   envOr("AGENTFOUNDRY_URL", "http://localhost:3000"),
		AgentFoundryKey:   os.Getenv("AGENTFOUNDRY_API_KEY"),
		AssistantAgentID:  os.Getenv("ASSISTANT_AGENT_ID"),
		TitleAgentID:      os.Getenv("TITLE_AGENT_ID"),
		DataDir:           envOr("DATA_DIR", "./data"),
		EmailPollInterval: durationEnv("EMAIL_POLL_INTERVAL", 60*time.Second),
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

func durationEnv(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return def
}
