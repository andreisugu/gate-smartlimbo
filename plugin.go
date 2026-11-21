package smartlimbo

import (
	"context"
	"path/filepath"

	"github.com/go-logr/logr"
	"github.com/robinbraemer/event"
	"go.minekube.com/gate/pkg/edition/java/proxy"
)

// Plugin is the Gate SmartLimbo failover and reconnection queue plugin.
var Plugin = proxy.Plugin{
	Name: "SmartLimbo",
	Init: func(ctx context.Context, p *proxy.Proxy) error {
		log := logr.FromContextOrDiscard(ctx)
		configPath := filepath.Join("config", "smartlimbo.toml")

		store := NewConfigStore(configPath)
		if _, err := store.Load(); err != nil {
			log.Error(err, "Failed to load smartlimbo configuration")
			return err
		}

		queue := NewQueueManager()
		handler := NewLimboHandler(p, store, queue, log)
		handler.Start(ctx)

		// 1. Kick Failover to Limbo / Notify on switch failure
		event.Subscribe(p.Event(), 0, func(e *proxy.KickedFromServerEvent) {
			handler.HandleKick(e)
		})

		// 2. Initial Connection Health Check & Failover
		event.Subscribe(p.Event(), 0, func(e *proxy.PlayerChooseInitialServerEvent) {
			handler.HandleInitialServer(e)
		})

		// 3. Clear queue when player connects to target or switches gameplay server
		event.Subscribe(p.Event(), 0, func(e *proxy.ServerPostConnectEvent) {
			handler.HandlePostConnect(e)
		})

		// 4. Protected Limbo Command Blocker
		event.Subscribe(p.Event(), 0, func(e *proxy.CommandExecuteEvent) {
			handler.HandleCommand(e)
		})

		// 5. Cleanup on Disconnect
		event.Subscribe(p.Event(), 0, func(e *proxy.DisconnectEvent) {
			handler.HandleDisconnect(e)
		})

		// 6. Register In-Game Commands
		RegisterCommands(p, store, queue, handler)

		log.Info("SmartLimbo loaded (smart failover, cached health probes, per-server queue & /hub active)")
		return nil
	},
}
