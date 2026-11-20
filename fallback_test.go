package smartlimbo

import (
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
		name         string
		fallbacks    []string
		healthy      []string
		exclude      string
		includeLimbo bool
		want         string
	}{
		{
			name:      "healthy first fallback",
			fallbacks: []string{"lobby", "backup"},
			healthy:   []string{"lobby:25565"},
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
			name:      "no fallback configured",
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
			name:      "leave skips limbo",
			fallbacks: []string{"limbo", "lobby"},
			healthy:   []string{"lobby:25565"},
			want:      "lobby",
		},
		{
			name:         "kick keeps limbo as always available",
			fallbacks:    []string{"limbo", "lobby"},
			includeLimbo: true,
			want:         "limbo",
		},
		{
			name:         "excluded fallback is skipped",
			fallbacks:    []string{"lobby", "backup"},
			healthy:      []string{"lobby:25565", "backup:25565"},
			exclude:      "lobby",
			includeLimbo: true,
			want:         "backup",
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

			got := findReachableFallback(cfg, lookup, reachable, tt.exclude, tt.includeLimbo)
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
