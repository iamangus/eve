package config

import (
	"fmt"
	"os"
)

type Config struct {
	Listen            string
	AgentFoundryURL   string
	AgentFoundryKey   string
	AssistantAgentID string
	TitleAgentID     string
}

func Load() (Config, error) {
	cfg := Config{
		Listen:           envOr("LISTEN", ":8090"),
		AgentFoundryURL:  envOr("AGENTFOUNDRY_URL", "http://localhost:3000"),
		AgentFoundryKey:  os.Getenv("AGENTFOUNDRY_API_KEY"),
		AssistantAgentID: os.Getenv("ASSISTANT_AGENT_ID"),
		TitleAgentID:     os.Getenv("TITLE_AGENT_ID"),
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