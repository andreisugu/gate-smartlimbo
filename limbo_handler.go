package smartlimbo

import (
	"context"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"go.minekube.com/gate/pkg/edition/java/proxy"
	"go.minekube.com/gate/pkg/util/uuid"
)

// LimboHandler manages kicks, failovers, background server pinging, and batch auto-reconnection.
type LimboHandler struct {
	proxy *proxy.Proxy
	store *ConfigStore
	queue *QueueManager
	log   logr.Logger
}

// NewLimboHandler creates a new LimboHandler instance.
func NewLimboHandler(p *proxy.Proxy, store *ConfigStore, queue *QueueManager, log logr.Logger) *LimboHandler {
	return &LimboHandler{
		proxy: p,
		store: store,
		queue: queue,
		log:   log,
	}
}

// Start begins the background queue worker and notification routines.
func (h *LimboHandler) Start(ctx context.Context) {
	// 1. Reconnect & Server Ping Worker
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

	// 2. Queue Position Notification Worker
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

// FindBestFallback finds the highest priority online fallback server from the configured list.
func (h *LimboHandler) FindBestFallback(excludeServer string) proxy.RegisteredServer {
	cfg := h.store.Get()
	if cfg == nil {
		return nil
	}

	exclude := strings.ToLower(strings.TrimSpace(excludeServer))
	timeout := time.Duration(cfg.PingTimeoutMs) * time.Millisecond

	// 1. Check fallback_servers in priority order
	for _, name := range cfg.FallbackServers {
		clean := strings.ToLower(strings.TrimSpace(name))
		if clean == exclude {
			continue
		}
		srv := h.proxy.Server(clean)
		if srv == nil {
			continue
		}
		// If it's the configured limbo server, it's considered always available
		if clean == strings.ToLower(cfg.LimboServer) {
			return srv
		}
		// Check TCP reachability
		addr := srv.ServerInfo().Addr().String()
		if isServerReachable(addr, timeout) {
			return srv
		}
	}

	// 2. Ultimate fallback to limbo_server if not already excluded
	limboName := strings.ToLower(cfg.LimboServer)
	if limboName != exclude {
		if srv := h.proxy.Server(cfg.LimboServer); srv != nil {
			return srv
		}
	}

	return nil
}

// HandleKick intercepts server kicks and moves the player to the best available fallback server + queue.
func (h *LimboHandler) HandleKick(e *proxy.KickedFromServerEvent) {
	cfg := h.store.Get()
	if cfg == nil {
		return
	}

	kickedServer := e.Server()
	kickedServerName := strings.ToLower(kickedServer.ServerInfo().Name())
	limboName := strings.ToLower(cfg.LimboServer)

	// Avoid redirect loops if kicked from Limbo itself
	if kickedServerName == limboName {
		return
	}

	targetFallback := h.FindBestFallback(kickedServerName)
	if targetFallback == nil {
		h.log.Info("No available fallback server found for kicked player", "kicked_server", kickedServerName)
		return
	}

	player := e.Player()
	pos := h.queue.Enqueue(kickedServerName, player)

	// Redirect player to fallback server (e.g. steelmc, or picolimbo)
	e.SetResult(&proxy.RedirectPlayerKickResult{
		Server:  targetFallback,
		Message: nil,
	})

	go func() {
		time.Sleep(200 * time.Millisecond) // Short delay to allow client state transition
		msgTemplate := cfg.Messages.KickedToLimbo
		msg := strings.ReplaceAll(msgTemplate, "%server%", kickedServer.ServerInfo().Name())
		msg = strings.ReplaceAll(msg, "%fallback%", targetFallback.ServerInfo().Name())
		msg = strings.ReplaceAll(msg, "%position%", strconv.Itoa(pos))
		_ = player.SendMessage(FormatText(msg))
	}()
}

// HandleInitialServer handles failover if the direct connect server is unavailable.
func (h *LimboHandler) HandleInitialServer(e *proxy.PlayerChooseInitialServerEvent) {
	cfg := h.store.Get()
	if cfg == nil {
		return
	}

	if e.InitialServer() == nil {
		if target := h.FindBestFallback(""); target != nil {
			e.SetInitialServer(target)
		}
	}
}

// HandleCommand intercepts commands executed while inside the protected Limbo server.
func (h *LimboHandler) HandleCommand(e *proxy.CommandExecuteEvent) {
	player, ok := e.Source().(proxy.Player)
	if !ok {
		return
	}

	cfg := h.store.Get()
	if cfg == nil || !cfg.ProtectedLimbo {
		return
	}

	cs := player.CurrentServer()
	if cs == nil {
		return
	}

	currentServerName := strings.ToLower(cs.Server().ServerInfo().Name())
	limboName := strings.ToLower(cfg.LimboServer)

	// Command blocking ONLY applies while inside the actual Limbo server (not gameplay backup servers like steelmc)
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
	h.queue.Remove(e.Player().ID())
}

// processQueues checks server availability and reconnects players in batches.
func (h *LimboHandler) processQueues(ctx context.Context) {
	cfg := h.store.Get()
	if cfg == nil {
		return
	}

	activeServers := h.queue.GetActiveTargetServers()
	if len(activeServers) == 0 {
		return
	}

	for _, srvName := range activeServers {
		targetServer := h.proxy.Server(srvName)
		if targetServer == nil {
			continue
		}

		addr := targetServer.ServerInfo().Addr().String()
		if !isServerReachable(addr, time.Duration(cfg.PingTimeoutMs)*time.Millisecond) {
			continue
		}

		// Target server is back online! Reconnect waiting players in batches
		batch := h.queue.GetNextBatch(srvName, cfg.ReconnectBatchSize)
		if len(batch) == 0 {
			continue
		}

		for _, qp := range batch {
			if !h.isPlayerConnected(qp.Player.ID()) {
				continue
			}

			go func(player proxy.Player, srv proxy.RegisteredServer, name string) {
				defer func() {
					_ = recover()
				}()
				if !player.Active() {
					return
				}

				req := player.CreateConnectionRequest(srv)
				res, err := req.Connect(ctx)
				if err == nil && res != nil && res.Status() == proxy.SuccessConnectionStatus {
					msg := strings.ReplaceAll(cfg.Messages.ReconnectSuccess, "%server%", srv.ServerInfo().Name())
					_ = player.SendMessage(FormatText(msg))
				} else {
					if player.Active() {
						// Connection rejected or not fully ready, keep in queue
						h.queue.Enqueue(name, player)
						_, pos, _, _ := h.queue.GetPosition(player.ID())
						failMsg := strings.ReplaceAll(cfg.Messages.ReconnectFailed, "%server%", srv.ServerInfo().Name())
						failMsg = strings.ReplaceAll(failMsg, "%position%", strconv.Itoa(pos))
						_ = player.SendMessage(FormatText(failMsg))
					}
				}
			}(qp.Player, targetServer, srvName)

			if cfg.ReconnectDelayMs > 0 {
				time.Sleep(time.Duration(cfg.ReconnectDelayMs) * time.Millisecond)
			}
		}
	}
}

// sendQueueNotifications sends periodic position updates via action bar or chat.
func (h *LimboHandler) sendQueueNotifications() {
	cfg := h.store.Get()
	if cfg == nil || cfg.NotifyMode == "none" {
		return
	}

	allPlayers := h.proxy.Players()
	for _, player := range allPlayers {
		if !player.Active() {
			continue
		}
		targetServer, pos, total, queued := h.queue.GetPosition(player.ID())
		if !queued {
			continue
		}

		srvDisplay := targetServer
		if srv := h.proxy.Server(targetServer); srv != nil {
			srvDisplay = srv.ServerInfo().Name()
		}

		// Action Bar Notification
		if cfg.NotifyMode == "action_bar" || cfg.NotifyMode == "both" {
			tmpl := cfg.Messages.QueueActionbar
			msg := strings.ReplaceAll(tmpl, "%server%", srvDisplay)
			msg = strings.ReplaceAll(msg, "%position%", strconv.Itoa(pos))
			msg = strings.ReplaceAll(msg, "%total%", strconv.Itoa(total))
			_ = player.SendActionBar(FormatText(msg))
		}

		// Chat Notification
		if cfg.NotifyMode == "chat" || cfg.NotifyMode == "both" {
			tmpl := cfg.Messages.QueueChat
			msg := strings.ReplaceAll(tmpl, "%server%", srvDisplay)
			msg = strings.ReplaceAll(msg, "%position%", strconv.Itoa(pos))
			msg = strings.ReplaceAll(msg, "%total%", strconv.Itoa(total))
			_ = player.SendMessage(FormatText(msg))
		}
	}
}

func (h *LimboHandler) isPlayerConnected(id uuid.UUID) bool {
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
