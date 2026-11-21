package smartlimbo

import (
	"sync/atomic"
	"testing"
	"time"

	"go.minekube.com/gate/pkg/edition/java/proxy"
)

type fakeRegisteredServer struct {
	info proxy.ServerInfo
}

func (f fakeRegisteredServer) ServerInfo() proxy.ServerInfo { return f.info }
func (f fakeRegisteredServer) Players() proxy.Players       { return nil }

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

func newFakeServer(name string) proxy.RegisteredServer {
	return fakeRegisteredServer{info: proxy.NewServerInfo(name, fakeAddr(name+":25565"))}
}

func TestFindReachableFallback(t *testing.T) {
	servers := map[string]proxy.RegisteredServer{
		"lobby":  newFakeServer("lobby"),
		"backup": newFakeServer("backup"),
		"limbo":  newFakeServer("limbo"),
	}
	lookup := func(name string) proxy.RegisteredServer { return servers[name] }

	tests := []struct {
		name      string
		fallbacks []string
		healthy   []string
		exclude   string
		skipLimbo bool
		want      string
	}{
		{
			name:      "healthy first fallback",
			fallbacks: []string{"lobby", "backup"},
			healthy:   []string{"lobby:25565"},
			want:      "lobby",
		},
		{
			name:      "first of two healthy fallbacks wins",
			fallbacks: []string{"lobby", "backup"},
			healthy:   []string{"lobby:25565", "backup:25565"},
			want:      "lobby",
		},
		{
			name:      "dead first fallback uses next",
			fallbacks: []string{"lobby", "backup"},
			healthy:   []string{"backup:25565"},
			want:      "backup",
		},
		{
			name:      "all fallbacks dead",
			fallbacks: []string{"lobby", "backup"},
			want:      "",
		},
		{
			name:      "no fallback configured still tries direct_connect_server none",
			fallbacks: nil,
			healthy:   []string{"lobby:25565"},
			want:      "",
		},
		{
			name:      "unregistered fallback is skipped",
			fallbacks: []string{"missing", "backup"},
			healthy:   []string{"backup:25565"},
			want:      "backup",
		},
		{
			name:      "healthy limbo can act as fallback when not skipped",
			fallbacks: []string{"limbo", "lobby"},
			healthy:   []string{"limbo:25565"},
			want:      "limbo",
		},
		{
			name:      "skipLimbo skips limbo even when healthy",
			fallbacks: []string{"limbo", "backup"},
			healthy:   []string{"limbo:25565", "backup:25565"},
			skipLimbo: true,
			want:      "backup",
		},
		{
			name:      "dead limbo is skipped instead of returned blindly",
			fallbacks: []string{"limbo", "backup"},
			healthy:   []string{"backup:25565"},
			want:      "backup",
		},
		{
			name:      "excluded fallback is skipped",
			fallbacks: []string{"lobby", "backup"},
			healthy:   []string{"lobby:25565", "backup:25565"},
			exclude:   "lobby",
			want:      "backup",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				LimboServer:     "limbo",
				FallbackServers: tt.fallbacks,
				PingTimeoutMs:   50,
			}
			reachable := func(addr string, _ time.Duration) bool {
				for _, healthy := range tt.healthy {
					if addr == healthy {
						return true
					}
				}
				return false
			}

			got := findReachableFallbackWithOptions(cfg, lookup, reachable, tt.exclude, tt.skipLimbo)
			if got == nil {
				if tt.want != "" {
					t.Fatalf("got nil, want %q", tt.want)
				}
				return
			}
			if got.ServerInfo().Name() != tt.want {
				t.Fatalf("got %q, want %q", got.ServerInfo().Name(), tt.want)
			}
		})
	}
}

func TestHealthProberCachingAndTTLExpiry(t *testing.T) {
	var probes atomic.Int32
	now := time.Unix(1000, 0)
	prober := NewHealthProber(
		func() time.Duration { return 2 * time.Second },
		func(addr string, _ time.Duration) bool {
			probes.Add(1)
			return true
		},
	)
	prober.now = func() time.Time { return now }

	if !prober.Check("lobby:25565", 50*time.Millisecond) {
		t.Fatal("expected initial probe true")
	}
	if !prober.Check("lobby:25565", 50*time.Millisecond) {
		t.Fatal("expected cached probe true")
	}
	if probes.Load() != 1 {
		t.Fatalf("expected 1 probe within TTL, got %d", probes.Load())
	}

	now = now.Add(3 * time.Second)
	_ = prober.Check("lobby:25565", 50*time.Millisecond)
	if probes.Load() != 2 {
		t.Fatalf("expected re-probe after TTL expiry, got %d", probes.Load())
	}

	prober.Invalidate("lobby:25565")
	_ = prober.Check("lobby:25565", 50*time.Millisecond)
	if probes.Load() != 3 {
		t.Fatalf("expected re-probe after Invalidate, got %d", probes.Load())
	}
}
