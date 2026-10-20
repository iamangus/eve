package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInvalidConfigurationFailsInsteadOfUsingDefaults(t *testing.T) {
	t.Setenv("AGENTFOUNDRY_API_KEY", "test")
	t.Setenv("ASSISTANT_AGENT_ID", "eve")
	for _, test := range []struct{ name, value string }{{"EMAIL_POLL_INTERVAL", "typo"}, {"SMTP_PORT", "70000"}, {"CONTEXT_TRIGGER_FRACTION", "NaN"}, {"AGENTFOUNDRY_URL", "/relative"}, {"PROACTIVE_ENABLED", "maybe"}} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(test.name, test.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid setting accepted")
			}
		})
	}
}

func TestSecretFileConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("test-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTFOUNDRY_API_KEY", "")
	t.Setenv("AGENTFOUNDRY_API_KEY_FILE", path)
	t.Setenv("ASSISTANT_AGENT_ID", "eve")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AgentFoundryKey != "test-key" {
		t.Fatal("secret file not loaded")
	}
	t.Setenv("AGENTFOUNDRY_API_KEY", "ambiguous")
	if _, err := Load(); err == nil {
		t.Fatal("ambiguous secret configuration accepted")
	}
}
