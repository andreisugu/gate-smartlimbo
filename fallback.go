package smartlimbo

import (
	"strings"
	"sync"
	"time"

	"go.minekube.com/gate/pkg/edition/java/proxy"
)

type healthCacheEntry struct {
	healthy   bool
	checkedAt time.Time
}

// HealthProber caches backend TCP health checks over a short configurable TTL.
type HealthProber struct {
	ttl   func() time.Duration
	probe func(string, time.Duration) bool
	now   func() time.Time

	mu    sync.Mutex
	cache map[string]healthCacheEntry
}

// NewHealthProber creates a HealthProber backed by the provided probe function and TTL callback.
func NewHealthProber(ttl func() time.Duration, probe func(string, time.Duration) bool) *HealthProber {
	if probe == nil {
		probe = isServerReachable
	}
	return &HealthProber{
		ttl:   ttl,
		probe: probe,
		now:   time.Now,
		cache: make(map[string]healthCacheEntry),
	}
}

// Check returns whether addr accepts a TCP connection, serving cached results within the TTL.
func (p *HealthProber) Check(addr string, timeout time.Duration) bool {
	if addr == "" {
		return false
	}
	var cacheTTL time.Duration
	if p.ttl != nil {
		cacheTTL = p.ttl()
	}
	if cacheTTL <= 0 {
		return p.probe(addr, timeout)
	}

	now := p.now()
	p.mu.Lock()
	entry, ok := p.cache[addr]
	p.mu.Unlock()
	if ok && now.Sub(entry.checkedAt) < cacheTTL {
		return entry.healthy
	}

	okProbe := p.probe(addr, timeout)
	p.mu.Lock()
	p.cache[addr] = healthCacheEntry{healthy: okProbe, checkedAt: p.now()}
	p.mu.Unlock()
	return okProbe
}

// Invalidate removes any cached health entry for addr.
func (p *HealthProber) Invalidate(addr string) {
	if addr == "" {
		return
	}
	p.mu.Lock()
	delete(p.cache, addr)
	p.mu.Unlock()
}

// findReachableFallback returns the first registered fallback server (or direct_connect_server)
// that accepts a TCP connection.
func findReachableFallbackWithOptions(
	cfg *Config,
	lookup func(string) proxy.RegisteredServer,
	reachable func(string, time.Duration) bool,
	exclude string,
	skipLimbo bool,
) proxy.RegisteredServer {
	if cfg == nil || lookup == nil || reachable == nil {
		return nil
	}

	exclude = strings.ToLower(strings.TrimSpace(exclude))
	limboName := strings.ToLower(strings.TrimSpace(cfg.LimboServer))
	timeout := time.Duration(cfg.PingTimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}

	seen := make(map[string]struct{}, len(cfg.FallbackServers)+1)
	candidates := make([]string, 0, len(cfg.FallbackServers)+1)
	appendCandidate := func(name string) {
		clean := strings.ToLower(strings.TrimSpace(name))
		if clean == "" || clean == exclude {
			return
		}
		if skipLimbo && clean == limboName {
			return
		}
		if _, exists := seen[clean]; exists {
			return
		}
		seen[clean] = struct{}{}
		candidates = append(candidates, clean)
	}

	for _, name := range cfg.FallbackServers {
		appendCandidate(name)
	}
	appendCandidate(cfg.DirectConnectServer)

	for _, clean := range candidates {
		srv := lookup(clean)
		if srv == nil || srv.ServerInfo() == nil || srv.ServerInfo().Addr() == nil {
			continue
		}
		if reachable(srv.ServerInfo().Addr().String(), timeout) {
			return srv
		}
	}

	return nil
}
