package smartlimbo

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	c "go.minekube.com/common/minecraft/component"
	"go.minekube.com/gate/pkg/edition/java/proxy"
	"go.minekube.com/gate/pkg/util/uuid"
)

// LimboHandler manages kicks, failovers, cached health probes, and batched auto-reconnection.
type LimboHandler struct {
	proxy         *proxy.Proxy
	store         *ConfigStore
	queue         *QueueManager
	log           logr.Logger
	prober        *HealthProber
	serverLookup  func(string) proxy.RegisteredServer
	activeBatches sync.Map // map[string]bool
}

// NewLimboHandler creates a new LimboHandler instance.
func NewLimboHandler(p *proxy.Proxy, store *ConfigStore, queue *QueueManager, log logr.Logger) *LimboHandler {
	return NewLimboHandlerWithProbe(p, store, queue, log, isServerReachable)
}

// NewLimboHandlerWithProbe creates a LimboHandler with a custom TCP health check probe (for testing).
func NewLimboHandlerWithProbe(
	p *proxy.Proxy,
	store *ConfigStore,
	queue *QueueManager,
	log logr.Logger,
	probe func(string, time.Duration) bool,
) *LimboHandler {
	ttlFn := func() time.Duration {
		if cfg := store.Get(); cfg != nil {
			if cfg.HealthCacheMs < 0 {
				return 0
			}
			if cfg.HealthCacheMs > 0 {
				return time.Duration(cfg.HealthCacheMs) * time.Millisecond
			}
		}
		return 1500 * time.Millisecond
	}
	return &LimboHandler{
		proxy:  p,
		store:  store,
		queue:  queue,
		log:    log,
		prober: NewHealthProber(ttlFn, probe),
		serverLookup: func(name string) proxy.RegisteredServer {
			if p == nil {
				return nil
			}
			return p.Server(name)
		},
	}
}

// ProbeAddr checks backend TCP reachability using the configured TTL cache.
func (h *LimboHandler) ProbeAddr(addr string, timeout time.Duration) bool {
	return h.prober.Check(addr, timeout)
}

// InvalidateProbe clears any cached reachability result for addr.
func (h *LimboHandler) InvalidateProbe(addr string) {
	h.prober.Invalidate(addr)
}

// Start begins the background queue worker and notification routines.
func (h *LimboHandler) Start(ctx context.Context) {
	go func() {
		for {
			interval := 3000 * time.Millisecond
			if cfg := h.store.Get(); cfg != nil && cfg.TaskIntervalMs > 0 {
				interval = time.Duration(cfg.TaskIntervalMs) * time.Millisecond
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
				h.processQueues(ctx)
			}
		}
	}()

	go func() {
		for {
			interval := 3000 * time.Millisecond
			if cfg := h.store.Get(); cfg != nil && cfg.QueueNotifyIntervalMs > 0 {
				interval = time.Duration(cfg.QueueNotifyIntervalMs) * time.Millisecond
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(interval):
				h.sendQueueNotifications()
			}
		}
	}()
}

func (h *LimboHandler) lookupServer(name string) proxy.RegisteredServer {
	if h.serverLookup != nil {
		return h.serverLookup(name)
	}
	if h.proxy != nil {
		return h.proxy.Server(name)
	}
	return nil
}

// FindBestFallback finds the highest priority online fallback server, falling back to Limbo
// if all configured playable fallbacks are down.
func (h *LimboHandler) FindBestFallback(excludeServer string) proxy.RegisteredServer {
	cfg := h.store.Get()
	if cfg == nil {
		return nil
	}

	// 1. Prefer playable fallbacks first.
	if target := findReachableFallbackWithOptions(cfg, h.lookupServer, h.ProbeAddr, excludeServer, true); target != nil {
		return target
	}

	// 2. Fall back to Limbo server if it is online and not excluded.
	exclude := strings.ToLower(strings.TrimSpace(excludeServer))
	limboName := strings.ToLower(strings.TrimSpace(cfg.LimboServer))
	if limboName != "" && limboName != exclude {
		if srv := h.lookupServer(cfg.LimboServer); srv != nil && h.isServerReachable(srv) {
			return srv
		}
	}

	return nil
}

// FindPlayableFallback returns the highest priority reachable non-limbo fallback server.
func (h *LimboHandler) FindPlayableFallback(excludeServer string) proxy.RegisteredServer {
	cfg := h.store.Get()
	if cfg == nil {
		return nil
	}
	return findReachableFallbackWithOptions(cfg, h.lookupServer, h.ProbeAddr, excludeServer, true)
}

func (h *LimboHandler) isServerReachable(srv proxy.RegisteredServer) bool {
	if srv == nil || srv.ServerInfo() == nil || srv.ServerInfo().Addr() == nil {
		return false
	}
	timeout := 1500 * time.Millisecond
	if cfg := h.store.Get(); cfg != nil && cfg.PingTimeoutMs > 0 {
		timeout = time.Duration(cfg.PingTimeoutMs) * time.Millisecond
	}
	return h.ProbeAddr(srv.ServerInfo().Addr().String(), timeout)
}

func (h *LimboHandler) isConfiguredFallback(cfg *Config, serverName string) bool {
	if cfg == nil {
		return false
	}
	clean := strings.ToLower(strings.TrimSpace(serverName))
	if clean == "" {
		return false
	}
	if strings.EqualFold(clean, strings.TrimSpace(cfg.DirectConnectServer)) {
		return true
	}
	for _, fb := range cfg.FallbackServers {
		if strings.EqualFold(clean, strings.TrimSpace(fb)) {
			return true
		}
	}
	return false
}

// ExtractComponentText flattens a Minecraft component tree to plain text.
func ExtractComponentText(comp c.Component) string {
	if comp == nil {
		return ""
	}
	var b strings.Builder
	switch v := comp.(type) {
	case *c.Text:
		b.WriteString(v.Content)
		for _, child := range v.Extra {
			b.WriteString(ExtractComponentText(child))
		}
	case *c.Translation:
		if v.Fallback != "" {
			b.WriteString(v.Fallback)
		} else {
			b.WriteString(v.Key)
		}
		for _, with := range v.With {
			text := ExtractComponentText(with)
			if text != "" {
				b.WriteString(" ")
				b.WriteString(text)
			}
		}
	}
	return b.String()
}

// IsKickReasonIgnored reports whether reason matches a non-recoverable kick pattern
// such as bans, whitelist denials, version mismatches, or full servers.
func IsKickReasonIgnored(cfg *Config, reason string) bool {
	if cfg == nil || len(cfg.IgnoreKickReasons) == 0 {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(reason))
	if lower == "" {
		return false
	}
	for _, pat := range cfg.IgnoreKickReasons {
		clean := strings.ToLower(strings.TrimSpace(pat))
		if clean != "" && strings.Contains(lower, clean) {
			return true
		}
	}
	return false
}

func formatKickMessage(template, serverName, fallbackName, reason string, pos int) string {
	msg := strings.ReplaceAll(template, "%server%", serverName)
	msg = strings.ReplaceAll(msg, "%fallback%", fallbackName)
	msg = strings.ReplaceAll(msg, "%position%", strconv.Itoa(pos))
	msg = strings.ReplaceAll(msg, "%reason%", reason)
	return msg
}

// HandleKick intercepts server kicks and routes recoverable outages to Limbo + queue
// while keeping players on their existing server when a server-switch attempt fails.
func (h *LimboHandler) HandleKick(e *proxy.KickedFromServerEvent) {
	if e == nil || e.Server() == nil || e.Server().ServerInfo() == nil || e.Player() == nil {
		return
	}
	cfg := h.store.Get()
	if cfg == nil {
		return
	}

	kickedServer := e.Server()
	kickedServerName := strings.ToLower(kickedServer.ServerInfo().Name())
	limboName := strings.ToLower(strings.TrimSpace(cfg.LimboServer))
	player := e.Player()

	if kickedServer.ServerInfo().Addr() != nil {
		h.InvalidateProbe(kickedServer.ServerInfo().Addr().String())
	}

	reasonText := strings.TrimSpace(ExtractComponentText(e.OriginalReason()))
	if reasonText == "" {
		reasonText = "Server closed or restarted"
	}
	ignored := IsKickReasonIgnored(cfg, reasonText)

	// Case A: Player failed to switch while already connected to another server.
	if e.KickedDuringServerConnect() {
		if cs := player.CurrentServer(); cs != nil && cs.Server() != nil && cs.Server().ServerInfo() != nil {
			curName := strings.ToLower(cs.Server().ServerInfo().Name())
			if curName != kickedServerName {
				if ignored {
					msg := formatKickMessage(cfg.Messages.ConnectRejected, kickedServer.ServerInfo().Name(), cs.Server().ServerInfo().Name(), reasonText, 0)
					e.SetResult(&proxy.NotifyKickResult{Message: FormatText(msg)})
					return
				}
				pos := h.queue.Enqueue(kickedServerName, player)
				msg := formatKickMessage(cfg.Messages.KickedToLimbo, kickedServer.ServerInfo().Name(), cs.Server().ServerInfo().Name(), reasonText, pos)
				e.SetResult(&proxy.NotifyKickResult{Message: FormatText(msg)})
				return
			}
		}
	}

	// Case B: Player was actively on kickedServer or had no active server.
	if kickedServerName == limboName {
		return
	}

	targetFallback := h.FindBestFallback(kickedServerName)
	if targetFallback == nil {
		h.log.Info("No available fallback server found for kicked player", "kicked_server", kickedServerName)
		return
	}

	if ignored {
		if strings.EqualFold(targetFallback.ServerInfo().Name(), limboName) {
			// Do not trap banned/unwhitelisted players inside Limbo.
			return
		}
		msg := formatKickMessage(cfg.Messages.ConnectRejected, kickedServer.ServerInfo().Name(), targetFallback.ServerInfo().Name(), reasonText, 0)
		e.SetResult(&proxy.RedirectPlayerKickResult{
			Server:  targetFallback,
			Message: FormatText(msg),
		})
		return
	}

	pos := h.queue.Enqueue(kickedServerName, player)
	msg := formatKickMessage(cfg.Messages.KickedToLimbo, kickedServer.ServerInfo().Name(), targetFallback.ServerInfo().Name(), reasonText, pos)
	e.SetResult(&proxy.RedirectPlayerKickResult{
		Server:  targetFallback,
		Message: FormatText(msg),
	})
}

// HandleInitialServer fails over initial connections when the primary server is unset or offline.
func (h *LimboHandler) HandleInitialServer(e *proxy.PlayerChooseInitialServerEvent) {
	if e == nil {
		return
	}
	if target := h.chooseInitialServer(e.InitialServer(), e.Player()); target != nil {
		e.SetInitialServer(target)
	}
}

func (h *LimboHandler) chooseInitialServer(initial proxy.RegisteredServer, player proxy.Player) proxy.RegisteredServer {
	cfg := h.store.Get()
	if cfg == nil {
		return nil
	}

	primaryName := strings.TrimSpace(cfg.DirectConnectServer)
	if initial == nil && primaryName != "" {
		initial = h.lookupServer(primaryName)
	}
	if initial != nil && initial.ServerInfo() != nil {
		primaryName = initial.ServerInfo().Name()
	}

	if initial != nil && h.isServerReachable(initial) {
		return initial
	}

	fallback := h.FindBestFallback(primaryName)
	if fallback != nil && fallback.ServerInfo() != nil {
		if primaryName != "" && strings.EqualFold(fallback.ServerInfo().Name(), cfg.LimboServer) && player != nil {
			h.queue.Enqueue(primaryName, player)
		}
		return fallback
	}
	return nil
}

// HandlePostConnect removes players from pending queues when they reach their target server
// or switch to another active gameplay backend.
func (h *LimboHandler) HandlePostConnect(e *proxy.ServerPostConnectEvent) {
	if e == nil || e.Player() == nil {
		return
	}
	cfg := h.store.Get()
	if cfg == nil {
		return
	}

	player := e.Player()
	cs := player.CurrentServer()
	if cs == nil || cs.Server() == nil || cs.Server().ServerInfo() == nil {
		return
	}
	currentServer := strings.ToLower(cs.Server().ServerInfo().Name())

	queuedTarget, _, _, queued := h.queue.GetPosition(player.ID())
	if !queued {
		return
	}

	if strings.EqualFold(currentServer, queuedTarget) {
		_, _ = h.queue.Remove(player.ID())
		return
	}

	if cfg.ShouldClearQueueOnSwitch() &&
		!strings.EqualFold(currentServer, cfg.LimboServer) &&
		!h.isConfiguredFallback(cfg, currentServer) {
		_, _ = h.queue.Remove(player.ID())
	}
}

// HandleCommand intercepts commands executed while inside the protected Limbo server.
func (h *LimboHandler) HandleCommand(e *proxy.CommandExecuteEvent) {
	if e == nil {
		return
	}
	player, ok := e.Source().(proxy.Player)
	if !ok {
		return
	}

	cfg := h.store.Get()
	if cfg == nil || !cfg.ProtectedLimbo {
		return
	}

	if player.HasPermission("smartlimbo.bypass") || player.HasPermission("smartlimbo.admin") || player.HasPermission("*") {
		return
	}

	cs := player.CurrentServer()
	if cs == nil || cs.Server() == nil || cs.Server().ServerInfo() == nil {
		return
	}

	currentServerName := strings.ToLower(cs.Server().ServerInfo().Name())
	limboName := strings.ToLower(strings.TrimSpace(cfg.LimboServer))
	if currentServerName != limboName {
		return
	}

	cmd := e.Command()
	if !h.store.IsCommandAllowed(cmd) {
		e.SetAllowed(false)
		e.SetForward(false)
		_ = player.SendMessage(FormatText(cfg.Messages.CommandBlocked))
	}
}

// HandleDisconnect cleans player from all queues upon logout.
func (h *LimboHandler) HandleDisconnect(e *proxy.DisconnectEvent) {
	if e == nil || e.Player() == nil {
		return
	}
	_, _ = h.queue.Remove(e.Player().ID())
}

// processQueues checks server availability and reconnects players in per-server batches.
func (h *LimboHandler) processQueues(ctx context.Context) {
	cfg := h.store.Get()
	if cfg == nil || h.proxy == nil {
		return
	}

	activeServers := h.queue.GetActiveTargetServers()
	if len(activeServers) == 0 {
		return
	}

	for _, srvName := range activeServers {
		targetServer := h.proxy.Server(srvName)
		if targetServer == nil || targetServer.ServerInfo() == nil || targetServer.ServerInfo().Addr() == nil {
			continue
		}

		addr := targetServer.ServerInfo().Addr().String()
		if !h.ProbeAddr(addr, time.Duration(cfg.PingTimeoutMs)*time.Millisecond) {
			continue
		}

		if _, alreadyRunning := h.activeBatches.LoadOrStore(srvName, true); alreadyRunning {
			continue
		}

		batch := h.queue.AcquireNextBatch(srvName, cfg.ReconnectBatchSize)
		if len(batch) == 0 {
			h.activeBatches.Delete(srvName)
			continue
		}

		go h.dispatchServerBatch(ctx, cfg, srvName, targetServer, addr, batch)
	}
}

func (h *LimboHandler) dispatchServerBatch(
	ctx context.Context,
	cfg *Config,
	srvName string,
	targetServer proxy.RegisteredServer,
	addr string,
	batch []*QueuedPlayer,
) {
	defer h.activeBatches.Delete(srvName)

	connectTimeout := time.Duration(cfg.ConnectTimeoutMs) * time.Millisecond
	if connectTimeout <= 0 {
		connectTimeout = 5 * time.Second
	}

	for idx, qp := range batch {
		if ctx.Err() != nil {
			h.queue.FinishConnect(qp.Player.ID(), false)
			continue
		}
		if !h.isPlayerConnected(qp.Player.ID()) {
			_, _ = h.queue.Remove(qp.Player.ID())
			continue
		}

		reqCtx, cancel := context.WithTimeout(ctx, connectTimeout)
		res, err := qp.Player.CreateConnectionRequest(targetServer).Connect(reqCtx)
		cancel()

		if err == nil && res != nil && res.Status() == proxy.SuccessConnectionStatus {
			h.queue.FinishConnect(qp.Player.ID(), true)
			msg := strings.ReplaceAll(cfg.Messages.ReconnectSuccess, "%server%", targetServer.ServerInfo().Name())
			_ = qp.Player.SendMessage(FormatText(msg))
		} else {
			h.InvalidateProbe(addr)
			if !qp.Player.Active() {
				_, _ = h.queue.Remove(qp.Player.ID())
			} else {
				h.queue.FinishConnect(qp.Player.ID(), false)
				_, pos, _, _ := h.queue.GetPosition(qp.Player.ID())
				failMsg := strings.ReplaceAll(cfg.Messages.ReconnectFailed, "%server%", targetServer.ServerInfo().Name())
				failMsg = strings.ReplaceAll(failMsg, "%position%", strconv.Itoa(pos))
				_ = qp.Player.SendMessage(FormatText(failMsg))
			}
		}

		if cfg.ReconnectDelayMs > 0 && idx+1 < len(batch) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(cfg.ReconnectDelayMs) * time.Millisecond):
			}
		}
	}
}

// sendQueueNotifications sends periodic position updates via action bar or chat.
func (h *LimboHandler) sendQueueNotifications() {
	cfg := h.store.Get()
	if cfg == nil || cfg.NotifyMode == "none" || h.proxy == nil {
		return
	}

	for _, player := range h.proxy.Players() {
		if !player.Active() {
			continue
		}
		targetServer, pos, total, queued := h.queue.GetPosition(player.ID())
		if !queued {
			continue
		}

		srvDisplay := targetServer
		if srv := h.proxy.Server(targetServer); srv != nil && srv.ServerInfo() != nil {
			srvDisplay = srv.ServerInfo().Name()
		}

		if cfg.NotifyMode == "action_bar" || cfg.NotifyMode == "both" {
			msg := formatQueueNotify(cfg.Messages.QueueActionbar, srvDisplay, pos, total)
			_ = player.SendActionBar(FormatText(msg))
		}
		if cfg.NotifyMode == "chat" || cfg.NotifyMode == "both" {
			msg := formatQueueNotify(cfg.Messages.QueueChat, srvDisplay, pos, total)
			_ = player.SendMessage(FormatText(msg))
		}
	}
}

func formatQueueNotify(tmpl, srvDisplay string, pos, total int) string {
	msg := strings.ReplaceAll(tmpl, "%server%", srvDisplay)
	msg = strings.ReplaceAll(msg, "%position%", strconv.Itoa(pos))
	msg = strings.ReplaceAll(msg, "%total%", strconv.Itoa(total))
	return msg
}

func (h *LimboHandler) isPlayerConnected(id uuid.UUID) bool {
	if h.proxy == nil {
		return false
	}
	p := h.proxy.Player(id)
	return p != nil && p.Active()
}

func isServerReachable(addr string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
