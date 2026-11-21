package smartlimbo

import (
	"testing"
	"time"

	"github.com/go-logr/logr"
	c "go.minekube.com/common/minecraft/component"
	"go.minekube.com/gate/pkg/edition/java/proxy"
)

func TestExtractComponentTextAndKickFiltering(t *testing.T) {
	comp := &c.Text{
		Content: "You are ",
		Extra: []c.Component{
			&c.Text{Content: "not whitelisted on this server!"},
		},
	}
	plain := ExtractComponentText(comp)
	if plain != "You are not whitelisted on this server!" {
		t.Fatalf("ExtractComponentText = %q", plain)
	}

	trans := &c.Translation{
		Key:      "multiplayer.disconnect.kicked",
		Fallback: "Disconnected:",
		With:     []c.Component{&c.Text{Content: "Banned by admin"}},
	}
	if got := ExtractComponentText(trans); got != "Disconnected: Banned by admin" {
		t.Fatalf("Translation ExtractComponentText = %q", got)
	}

	cfg := &Config{}
	applyConfigDefaults(cfg)

	if !IsKickReasonIgnored(cfg, plain) {
		t.Fatalf("expected whitelist kick to be ignored by auto-reconnect")
	}
	if !IsKickReasonIgnored(cfg, "Banned from server: griefing") {
		t.Fatalf("expected ban kick to be ignored")
	}
	if IsKickReasonIgnored(cfg, "Server closed") {
		t.Fatalf("expected 'Server closed' to trigger Limbo failover + queue")
	}

	cfgDisable := &Config{IgnoreKickReasons: []string{}}
	applyConfigDefaults(cfgDisable)
	if IsKickReasonIgnored(cfgDisable, "You are banned") {
		t.Fatalf("expected empty ignore_kick_reasons to allow all kicks")
	}
}

func TestHandleInitialServerFailsOverPrePopulatedOfflinePrimary(t *testing.T) {
	lobby := newFakeServer("lobby")
	nanolimbo := newFakeServer("nanolimbo")
	servers := map[string]proxy.RegisteredServer{
		"lobby":     lobby,
		"nanolimbo": nanolimbo,
	}

	store := NewConfigStore("")
	store.Set(&Config{
		LimboServer:         "nanolimbo",
		DirectConnectServer: "lobby",
		FallbackServers:     []string{"lobby"},
		PingTimeoutMs:       50,
		HealthCacheMs:       -1,
	})
	queue := NewQueueManager()
	healthyAddrs := map[string]bool{"nanolimbo:25565": true}

	h := NewLimboHandlerWithProbe(nil, store, queue, logr.Discard(), func(addr string, _ time.Duration) bool {
		return healthyAddrs[addr]
	})
	h.serverLookup = func(name string) proxy.RegisteredServer {
		return servers[name]
	}

	player := newMockPlayer("Alice")
	chosen := h.chooseInitialServer(lobby, player)
	if chosen == nil || chosen.ServerInfo().Name() != "nanolimbo" {
		t.Fatalf("expected failover to nanolimbo when initial server offline, got %v", chosen)
	}
	target, pos, total, queued := queue.GetPosition(player.ID())
	if !queued || target != "lobby" || pos != 1 || total != 1 {
		t.Fatalf("expected Alice queued for offline initial lobby (#1/1), got target=%q pos=%d/%d queued=%v", target, pos, total, queued)
	}

	healthyAddrs["lobby:25565"] = true
	bob := newMockPlayer("Bob")
	chosenBob := h.chooseInitialServer(lobby, bob)
	if chosenBob == nil || chosenBob.ServerInfo().Name() != "lobby" {
		t.Fatalf("expected online lobby to be kept, got %q", chosenBob.ServerInfo().Name())
	}
	if _, _, _, q := queue.GetPosition(bob.ID()); q {
		t.Fatalf("did not expect Bob to be queued when lobby is healthy")
	}
}
