package smartlimbo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigStoreLoadAndWhitelist(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "smartlimbo.toml")

	testTOML := `
limbo_server = "nanolimbo"
direct_connect_server = "hub"
fallback_servers = ["hub", "lobby"]
task_interval_ms = 2000
queue_notify_interval_ms = 2000
notify_mode = "action_bar"
reconnect_batch_size = 5
protected_limbo = true
allowed_commands = ["reconnect", "rc", "queue", "leave", "hub"]
`
	if err := os.WriteFile(configPath, []byte(testTOML), 0o644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	store := NewConfigStore(configPath)
	cfg, err := store.Load()
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if cfg.LimboServer != "nanolimbo" {
		t.Errorf("Expected limbo_server 'nanolimbo', got %s", cfg.LimboServer)
	}
	if cfg.ReconnectBatchSize != 5 {
		t.Errorf("Expected batch size 5, got %d", cfg.ReconnectBatchSize)
	}

	// Test command filtering
	if !store.IsCommandAllowed("/reconnect") {
		t.Errorf("Expected /reconnect to be allowed")
	}
	if !store.IsCommandAllowed("queue leave") {
		t.Errorf("Expected 'queue leave' to be allowed")
	}
	if store.IsCommandAllowed("/server survival") {
		t.Errorf("Expected /server survival to be blocked in Limbo")
	}
	if store.IsCommandAllowed("/pay steve 100") {
		t.Errorf("Expected /pay to be blocked in Limbo")
	}
}
