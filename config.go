package smartlimbo

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

var defaultIgnoreKickReasons = []string{
	"ban",
	"banned",
	"blacklist",
	"whitelist",
	"not whitelisted",
	"outdated",
	"incompatible",
	"server is full",
	"afk",
	"idle",
}

const defaultConfigTemplate = `# =============================================================================
#                 Gate SmartLimbo Configuration
# =============================================================================
# Smart Limbo failover, per-server auto-reconnect queues, and protected sandbox.
# =============================================================================

# The backend server name used as Limbo (e.g. nanolimbo, picolimbo, limbo)
limbo_server = "nanolimbo"

# Where to send initial player connections if their target server is offline
direct_connect_server = "lobby"

# Fallback servers when a player leaves the queue or uses /hub / /lobby
fallback_servers = ["lobby"]

# -----------------------------------------------------------------------------
# Reconnection Queue Settings
# -----------------------------------------------------------------------------
# How often to check backend server availability (in milliseconds)
task_interval_ms = 3000

# How often to send queue position updates to waiting players (in milliseconds)
queue_notify_interval_ms = 3000

# Where queue notifications are shown: "action_bar", "chat", "both", or "none"
notify_mode = "action_bar"

# How many players to reconnect per tick when a server comes back up
reconnect_batch_size = 3

# Delay between batches in milliseconds (prevents server join spikes)
reconnect_delay_ms = 500

# Backend TCP ping timeout in milliseconds
ping_timeout_ms = 1500

# Timeout for backend connection transfers in milliseconds
connect_timeout_ms = 5000

# TTL for cached backend TCP health checks in milliseconds
health_cache_ms = 1500

# Clear any pending reconnect queue if the player connects to another gameplay server
clear_queue_on_server_switch = true

# Kick reasons (case-insensitive substrings) that should NOT trigger auto-reconnect
ignore_kick_reasons = [
    "ban",
    "banned",
    "blacklist",
    "whitelist",
    "not whitelisted",
    "outdated",
    "incompatible",
    "server is full",
    "afk",
    "idle",
]

# -----------------------------------------------------------------------------
# Protected Limbo Sandbox
# -----------------------------------------------------------------------------
# Block game commands while players are in the Limbo server
protected_limbo = true

# Allowed commands while in Limbo (lowercase without leading slash)
allowed_commands = ["reconnect", "rc", "queue", "leave", "hub", "lobby", "help", "msg"]

# -----------------------------------------------------------------------------
# Customizable Messages (MiniMessage & RGB supported)
# -----------------------------------------------------------------------------
[messages]
kicked_to_limbo = "<yellow>⚠ <aqua>%server%</aqua> went offline or restarted.\n<gray>You were moved to Limbo and placed in queue <gold>#%position%</gold>.</gray></yellow>"
queue_actionbar = "<yellow>⏳ Waiting for <aqua>%server%</aqua> <dark_gray>•</dark_gray> Position: <gold>#%position%</gold>/<gold>%total%</gold></yellow>"
queue_chat = "<gray>[SmartLimbo] Reconnecting to <aqua>%server%</aqua> (Position <gold>#%position%</gold> of <gold>%total%</gold>)</gray>"
server_online = "<green>✔ <aqua>%server%</aqua> is back online! Reconnecting now...</green>"
reconnect_success = "<green>✔ Reconnected to <aqua>%server%</aqua>!</green>"
reconnect_failed = "<red>✖ <aqua>%server%</aqua> is not ready yet. Still waiting in queue (#%position%)...</red>"
left_queue = "<yellow>You left the queue and returned to <aqua>%server%</aqua>.</yellow>"
not_in_queue = "<gray>You are not currently in any reconnect queue.</gray>"
queue_info = "<aqua>════ SmartLimbo Queue Info ════</aqua>\n<gray>Target Server:</gray> <yellow>%server%</yellow>\n<gray>Your Position:</gray> <gold>#%position%</gold> <gray>of</gray> <gold>%total%</gold>\n<gray>Server Status:</gray> <yellow>%status%</yellow>"
command_blocked = "<red>✖ Commands are blocked in Limbo.\n<gray>Use <yellow>/leave</yellow> to exit or <yellow>/reconnect</yellow> to retry.</gray></red>"
already_in_hub = "<yellow>You are already connected to <aqua>%server%</aqua>!</yellow>"
connecting_hub = "<green>✔ Transferring you to <aqua>%server%</aqua>...</green>"
no_fallback_available = "<red>✖ No fallback or lobby server is currently reachable.</red>"
connect_rejected = "<red>✖ Could not connect to <aqua>%server%</aqua>: <gray>%reason%</gray></red>"
`

// Messages holds user-customizable chat and action bar notifications.
type Messages struct {
	KickedToLimbo       string `toml:"kicked_to_limbo"`
	QueueActionbar      string `toml:"queue_actionbar"`
	QueueChat           string `toml:"queue_chat"`
	ServerOnline        string `toml:"server_online"`
	ReconnectSuccess    string `toml:"reconnect_success"`
	ReconnectFailed     string `toml:"reconnect_failed"`
	LeftQueue           string `toml:"left_queue"`
	NotInQueue          string `toml:"not_in_queue"`
	QueueInfo           string `toml:"queue_info"`
	CommandBlocked      string `toml:"command_blocked"`
	AlreadyInHub        string `toml:"already_in_hub"`
	ConnectingHub       string `toml:"connecting_hub"`
	NoFallbackAvailable string `toml:"no_fallback_available"`
	ConnectRejected     string `toml:"connect_rejected"`
}

// Config represents the complete SmartLimbo configuration.
type Config struct {
	LimboServer              string   `toml:"limbo_server"`
	DirectConnectServer      string   `toml:"direct_connect_server"`
	FallbackServers          []string `toml:"fallback_servers"`
	TaskIntervalMs           int      `toml:"task_interval_ms"`
	QueueNotifyIntervalMs    int      `toml:"queue_notify_interval_ms"`
	NotifyMode               string   `toml:"notify_mode"`
	ReconnectBatchSize       int      `toml:"reconnect_batch_size"`
	ReconnectDelayMs         int      `toml:"reconnect_delay_ms"`
	PingTimeoutMs            int      `toml:"ping_timeout_ms"`
	ConnectTimeoutMs         int      `toml:"connect_timeout_ms"`
	HealthCacheMs            int      `toml:"health_cache_ms"`
	IgnoreKickReasons        []string `toml:"ignore_kick_reasons"`
	ClearQueueOnServerSwitch *bool    `toml:"clear_queue_on_server_switch"`
	ProtectedLimbo           bool     `toml:"protected_limbo"`
	AllowedCommands          []string `toml:"allowed_commands"`
	Messages                 Messages `toml:"messages"`
}

// ShouldClearQueueOnSwitch returns whether transferring to a non-limbo backend cancels pending queues.
func (c *Config) ShouldClearQueueOnSwitch() bool {
	if c == nil || c.ClearQueueOnServerSwitch == nil {
		return true
	}
	return *c.ClearQueueOnServerSwitch
}

// ConfigStore manages thread-safe configuration reading and updating.
type ConfigStore struct {
	path string
	mu   sync.RWMutex
	cfg  *Config
}

// NewConfigStore creates a new ConfigStore instance.
func NewConfigStore(path string) *ConfigStore {
	return &ConfigStore{path: path}
}

// Load reads and parses the configuration file.
func (cs *ConfigStore) Load() (*Config, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if _, err := os.Stat(cs.path); os.IsNotExist(err) {
		dir := filepath.Dir(cs.path)
		if dir != "." && dir != "" {
			_ = os.MkdirAll(dir, 0o755)
		}
		if err := os.WriteFile(cs.path, []byte(defaultConfigTemplate), 0o644); err != nil {
			return nil, fmt.Errorf("failed to create default smartlimbo config: %w", err)
		}
	}

	data, err := os.ReadFile(cs.path)
	if err != nil {
		return nil, fmt.Errorf("failed to read smartlimbo config %q: %w", cs.path, err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse TOML config %q: %w", cs.path, err)
	}

	applyConfigDefaults(&cfg)
	cs.cfg = &cfg
	return &cfg, nil
}

// Set stores an in-memory config (primarily for testing), applying defaults.
func (cs *ConfigStore) Set(cfg *Config) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cfg != nil {
		applyConfigDefaults(cfg)
	}
	cs.cfg = cfg
}

func applyConfigDefaults(cfg *Config) {
	if cfg.LimboServer == "" {
		cfg.LimboServer = "nanolimbo"
	}
	if cfg.DirectConnectServer == "" {
		cfg.DirectConnectServer = "lobby"
	}
	if len(cfg.FallbackServers) == 0 {
		cfg.FallbackServers = []string{"lobby"}
	}
	if cfg.TaskIntervalMs <= 0 {
		cfg.TaskIntervalMs = 3000
	}
	if cfg.QueueNotifyIntervalMs <= 0 {
		cfg.QueueNotifyIntervalMs = 3000
	}
	if cfg.NotifyMode == "" {
		cfg.NotifyMode = "action_bar"
	}
	if cfg.ReconnectBatchSize <= 0 {
		cfg.ReconnectBatchSize = 3
	}
	if cfg.ReconnectDelayMs < 0 {
		cfg.ReconnectDelayMs = 500
	}
	if cfg.PingTimeoutMs <= 0 {
		cfg.PingTimeoutMs = 1500
	}
	if cfg.ConnectTimeoutMs <= 0 {
		cfg.ConnectTimeoutMs = 5000
	}
	if cfg.HealthCacheMs == 0 {
		cfg.HealthCacheMs = 1500
	}
	if cfg.IgnoreKickReasons == nil {
		cfg.IgnoreKickReasons = slices.Clone(defaultIgnoreKickReasons)
	} else {
		reasons := make([]string, 0, len(cfg.IgnoreKickReasons))
		for _, r := range cfg.IgnoreKickReasons {
			if clean := strings.ToLower(strings.TrimSpace(r)); clean != "" {
				reasons = append(reasons, clean)
			}
		}
		cfg.IgnoreKickReasons = reasons
	}

	if cfg.Messages.AlreadyInHub == "" {
		cfg.Messages.AlreadyInHub = "<yellow>You are already connected to <aqua>%server%</aqua>!</yellow>"
	}
	if cfg.Messages.ConnectingHub == "" {
		cfg.Messages.ConnectingHub = "<green>✔ Transferring you to <aqua>%server%</aqua>...</green>"
	}
	if cfg.Messages.NoFallbackAvailable == "" {
		cfg.Messages.NoFallbackAvailable = "<red>✖ No fallback or lobby server is currently reachable.</red>"
	}
	if cfg.Messages.ConnectRejected == "" {
		cfg.Messages.ConnectRejected = "<red>✖ Could not connect to <aqua>%server%</aqua>: <gray>%reason%</gray></red>"
	}
	if cfg.Messages.NotInQueue == "" {
		cfg.Messages.NotInQueue = "<gray>You are not currently in any reconnect queue.</gray>"
	}
	if cfg.Messages.CommandBlocked == "" {
		cfg.Messages.CommandBlocked = "<red>✖ Commands are blocked in Limbo.\n<gray>Use <yellow>/leave</yellow> to exit or <yellow>/reconnect</yellow> to retry.</gray></red>"
	}

	normalizedCmds := make([]string, 0, len(cfg.AllowedCommands))
	for _, cmd := range cfg.AllowedCommands {
		clean := normalizeCommandRoot(cmd)
		if clean != "" {
			normalizedCmds = append(normalizedCmds, clean)
		}
	}
	cfg.AllowedCommands = normalizedCmds
}

func normalizeCommandRoot(raw string) string {
	clean := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "/"))
	parts := strings.Fields(clean)
	if len(parts) == 0 {
		return ""
	}
	root := parts[0]
	if idx := strings.IndexByte(root, ':'); idx >= 0 {
		root = root[idx+1:]
	}
	return root
}

// Get returns the current Config snapshot.
func (cs *ConfigStore) Get() *Config {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	return cs.cfg
}

// IsCommandAllowed checks if a command name is allowed in Limbo.
func (cs *ConfigStore) IsCommandAllowed(cmd string) bool {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	if cs.cfg == nil || !cs.cfg.ProtectedLimbo {
		return true
	}

	rootCmd := normalizeCommandRoot(cmd)
	if rootCmd == "" {
		return true
	}
	if rootCmd == "smartlimbo" || rootCmd == "slimbo" {
		return true
	}

	for _, allowed := range cs.cfg.AllowedCommands {
		if rootCmd == allowed {
			return true
		}
	}
	return false
}
