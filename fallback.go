package smartlimbo

import (
	"strings"
	"time"

	"go.minekube.com/gate/pkg/edition/java/proxy"
)

// findReachableFallback returns the first registered fallback server in
// priority order that accepts a TCP connection. The configured limbo server is
// treated as always reachable when includeLimbo is true, matching the kick
// failover path. When includeLimbo is false, limbo is skipped; /leave already
// runs inside limbo and must not reconnect to it.
func findReachableFallback(
	cfg *Config,
	lookup func(string) proxy.RegisteredServer,
	reachable func(string, time.Duration) bool,
	exclude string,
	includeLimbo bool,
) proxy.RegisteredServer {
	if cfg == nil || lookup == nil || reachable == nil {
		return nil
	}

	exclude = strings.ToLower(strings.TrimSpace(exclude))
	limboName := strings.ToLower(strings.TrimSpace(cfg.LimboServer))
	timeout := time.Duration(cfg.PingTimeoutMs) * time.Millisecond

	for _, name := range cfg.FallbackServers {
		clean := strings.ToLower(strings.TrimSpace(name))
		if clean == exclude {
			continue
		}
		if !includeLimbo && clean == limboName {
			continue
		}

		srv := lookup(clean)
		if srv == nil {
			continue
		}
		if clean == limboName {
			return srv
		}

		info := srv.ServerInfo()
		if info == nil {
			continue
		}
		addr := info.Addr()
		if addr == nil {
			continue
		}
		if reachable(addr.String(), timeout) {
			return srv
		}
	}

	return nil
}
