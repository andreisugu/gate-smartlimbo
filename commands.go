package smartlimbo

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.minekube.com/brigodier"
	c "go.minekube.com/common/minecraft/component"
	"go.minekube.com/gate/pkg/command"
	"go.minekube.com/gate/pkg/edition/java/proxy"
)

// RegisterCommands registers /reconnect, /rc, /queue, /leave, /hub, /lobby, and /smartlimbo commands.
func RegisterCommands(p *proxy.Proxy, store *ConfigStore, queue *QueueManager, handler *LimboHandler) {
	probeAddr := isServerReachable
	if handler != nil {
		probeAddr = handler.ProbeAddr
	}

	// -------------------------------------------------------------------------
	// 1. /reconnect & /rc
	// -------------------------------------------------------------------------
	reconnectCmd := command.Command(func(cmdCtx *command.Context) error {
		player, ok := cmdCtx.Source.(proxy.Player)
		if !ok {
			return cmdCtx.Source.SendMessage(&c.Text{Content: "§cOnly in-game players can reconnect."})
		}

		cfg := store.Get()
		if cfg == nil {
			return nil
		}

		targetServerName, _, _, queued := queue.GetPosition(player.ID())
		if !queued {
			return player.SendMessage(FormatText(cfg.Messages.NotInQueue))
		}

		targetServer := p.Server(targetServerName)
		if targetServer == nil {
			return player.SendMessage(&c.Text{Content: fmt.Sprintf("§cServer %q is not registered.", targetServerName)})
		}

		if !queue.TryStartConnect(player.ID()) {
			return player.SendMessage(&c.Text{Content: "§e[SmartLimbo] Reconnection attempt already in progress..."})
		}

		_ = player.SendMessage(&c.Text{Content: "§e[SmartLimbo] Attempting immediate reconnection..."})

		go func() {
			connectTimeout := time.Duration(cfg.ConnectTimeoutMs) * time.Millisecond
			if connectTimeout <= 0 {
				connectTimeout = 5 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
			defer cancel()

			res, err := player.CreateConnectionRequest(targetServer).Connect(ctx)
			succeeded := err == nil && res != nil && res.Status() == proxy.SuccessConnectionStatus
			queue.FinishConnect(player.ID(), succeeded)

			if succeeded {
				msg := strings.ReplaceAll(cfg.Messages.ReconnectSuccess, "%server%", targetServer.ServerInfo().Name())
				_ = player.SendMessage(FormatText(msg))
			} else {
				if handler != nil && targetServer.ServerInfo() != nil && targetServer.ServerInfo().Addr() != nil {
					handler.InvalidateProbe(targetServer.ServerInfo().Addr().String())
				}
				msg := strings.ReplaceAll(cfg.Messages.ReconnectFailed, "%server%", targetServer.ServerInfo().Name())
				_, pos, _, _ := queue.GetPosition(player.ID())
				msg = strings.ReplaceAll(msg, "%position%", strconv.Itoa(pos))
				_ = player.SendMessage(FormatText(msg))
			}
		}()

		return nil
	})

	p.Command().Register(brigodier.Literal("reconnect").Executes(reconnectCmd))
	p.Command().Register(brigodier.Literal("rc").Executes(reconnectCmd))

	// -------------------------------------------------------------------------
	// 2. /hub & /lobby (always send player to a playable fallback server)
	// -------------------------------------------------------------------------
	hubCmd := command.Command(func(cmdCtx *command.Context) error {
		player, ok := cmdCtx.Source.(proxy.Player)
		if !ok {
			return cmdCtx.Source.SendMessage(&c.Text{Content: "§cOnly in-game players can use /hub."})
		}

		cfg := store.Get()
		if cfg == nil {
			return nil
		}

		currentServer := ""
		if cs := player.CurrentServer(); cs != nil && cs.Server() != nil && cs.Server().ServerInfo() != nil {
			currentServer = cs.Server().ServerInfo().Name()
		}

		_, wasQueued := queue.Remove(player.ID())

		fallback := findReachableFallbackWithOptions(cfg, p.Server, probeAddr, currentServer, true)
		if fallback == nil {
			// Check if player is already standing in a configured playable fallback/hub server.
			if currentServer != "" && !strings.EqualFold(currentServer, cfg.LimboServer) {
				isHub := strings.EqualFold(currentServer, cfg.DirectConnectServer)
				if !isHub {
					for _, fb := range cfg.FallbackServers {
						if strings.EqualFold(currentServer, strings.TrimSpace(fb)) {
							isHub = true
							break
						}
					}
				}
				if isHub {
					msg := strings.ReplaceAll(cfg.Messages.AlreadyInHub, "%server%", currentServer)
					return player.SendMessage(FormatText(msg))
				}
			}
			return player.SendMessage(FormatText(cfg.Messages.NoFallbackAvailable))
		}

		connectTimeout := time.Duration(cfg.ConnectTimeoutMs) * time.Millisecond
		if connectTimeout <= 0 {
			connectTimeout = 5 * time.Second
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
			defer cancel()
			_, _ = player.CreateConnectionRequest(fallback).Connect(ctx)
		}()

		tmpl := cfg.Messages.ConnectingHub
		if wasQueued {
			tmpl = cfg.Messages.LeftQueue
		}
		msg := strings.ReplaceAll(tmpl, "%server%", fallback.ServerInfo().Name())
		return player.SendMessage(FormatText(msg))
	})

	p.Command().Register(brigodier.Literal("hub").Executes(hubCmd))
	p.Command().Register(brigodier.Literal("lobby").Executes(hubCmd))

	// -------------------------------------------------------------------------
	// 3. /queue and /queue leave / /leave / info
	// -------------------------------------------------------------------------
	leaveQueueFunc := func(cmdCtx *command.Context) error {
		player, ok := cmdCtx.Source.(proxy.Player)
		if !ok {
			return cmdCtx.Source.SendMessage(&c.Text{Content: "§cOnly in-game players can leave the queue."})
		}

		cfg := store.Get()
		if cfg == nil {
			return nil
		}

		currentServer := ""
		inLimbo := false
		if cs := player.CurrentServer(); cs != nil && cs.Server() != nil && cs.Server().ServerInfo() != nil {
			currentServer = cs.Server().ServerInfo().Name()
			inLimbo = strings.EqualFold(currentServer, cfg.LimboServer)
		}

		_, queued := queue.Remove(player.ID())
		if !queued && !inLimbo {
			return player.SendMessage(FormatText(cfg.Messages.NotInQueue))
		}

		fallback := findReachableFallbackWithOptions(cfg, p.Server, probeAddr, currentServer, true)
		if fallback != nil {
			connectTimeout := time.Duration(cfg.ConnectTimeoutMs) * time.Millisecond
			if connectTimeout <= 0 {
				connectTimeout = 5 * time.Second
			}
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
				defer cancel()
				_, _ = player.CreateConnectionRequest(fallback).Connect(ctx)
			}()
			msg := strings.ReplaceAll(cfg.Messages.LeftQueue, "%server%", fallback.ServerInfo().Name())
			return player.SendMessage(FormatText(msg))
		}

		if inLimbo && !queued {
			return player.SendMessage(FormatText(cfg.Messages.NoFallbackAvailable))
		}
		return player.SendMessage(&c.Text{Content: "§e[SmartLimbo] Removed from reconnect queue."})
	}

	queueInfoFunc := func(cmdCtx *command.Context) error {
		player, ok := cmdCtx.Source.(proxy.Player)
		if !ok {
			return cmdCtx.Source.SendMessage(&c.Text{Content: "§cOnly players can view queue info."})
		}

		cfg := store.Get()
		if cfg == nil {
			return nil
		}

		targetServerName, pos, total, queued := queue.GetPosition(player.ID())
		if !queued {
			return player.SendMessage(FormatText(cfg.Messages.NotInQueue))
		}

		status := "<red>Offline / Restarting</red>"
		if targetSrv := p.Server(targetServerName); targetSrv != nil && targetSrv.ServerInfo() != nil && targetSrv.ServerInfo().Addr() != nil {
			if probeAddr(targetSrv.ServerInfo().Addr().String(), time.Duration(cfg.PingTimeoutMs)*time.Millisecond) {
				status = "<green>Online (Processing Queue)</green>"
			}
		}

		msg := strings.ReplaceAll(cfg.Messages.QueueInfo, "%server%", targetServerName)
		msg = strings.ReplaceAll(msg, "%position%", strconv.Itoa(pos))
		msg = strings.ReplaceAll(msg, "%total%", strconv.Itoa(total))
		msg = strings.ReplaceAll(msg, "%status%", status)

		return player.SendMessage(FormatText(msg))
	}

	p.Command().Register(
		brigodier.Literal("queue").
			Executes(command.Command(queueInfoFunc)).
			Then(brigodier.Literal("leave").Executes(command.Command(leaveQueueFunc))).
			Then(brigodier.Literal("info").Executes(command.Command(queueInfoFunc))),
	)
	p.Command().Register(brigodier.Literal("leave").Executes(command.Command(leaveQueueFunc)))

	// -------------------------------------------------------------------------
	// 4. /smartlimbo admin command
	// -------------------------------------------------------------------------
	buildAdminCmd := func(alias string) brigodier.LiteralNodeBuilder {
		return brigodier.Literal(alias).
			Requires(command.Requires(func(c *command.RequiresContext) bool {
				return c.Source.HasPermission("smartlimbo.admin") || c.Source.HasPermission("smartlimbo.reload") || c.Source.HasPermission("smartlimbo.status") || c.Source.HasPermission("*")
			})).
			Executes(command.Command(func(cmdCtx *command.Context) error {
				return cmdCtx.Source.SendMessage(&c.Text{
					Content: "§b[SmartLimbo for Gate] §7by Andrei\n" +
						"§7Commands: §f/" + alias + " reload§7, §f/" + alias + " status§7, §f/reconnect§7, §f/queue info§7, §f/hub",
				})
			})).
			Then(brigodier.Literal("reload").
				Requires(command.Requires(func(c *command.RequiresContext) bool {
					return c.Source.HasPermission("smartlimbo.reload") || c.Source.HasPermission("smartlimbo.admin") || c.Source.HasPermission("*")
				})).
				Executes(command.Command(func(cmdCtx *command.Context) error {
					if _, err := store.Load(); err != nil {
						return cmdCtx.Source.SendMessage(&c.Text{Content: fmt.Sprintf("§c[SmartLimbo] Reload failed: %v", err)})
					}
					return cmdCtx.Source.SendMessage(&c.Text{Content: "§a[SmartLimbo] Configuration reloaded successfully!"})
				})),
			).
			Then(brigodier.Literal("status").
				Requires(command.Requires(func(c *command.RequiresContext) bool {
					return c.Source.HasPermission("smartlimbo.status") || c.Source.HasPermission("smartlimbo.admin") || c.Source.HasPermission("*")
				})).
				Executes(command.Command(func(cmdCtx *command.Context) error {
					cfg := store.Get()
					summary := queue.GetAllQueuesSummary()
					var b strings.Builder
					b.WriteString(fmt.Sprintf("§b[SmartLimbo Status]\n§7Limbo Server: §f%s\n§7Total Queued: §f%d\n", cfg.LimboServer, queue.TotalQueuedPlayers()))
					if len(summary) == 0 {
						b.WriteString("§7Active Queues: §8(none)\n")
					} else {
						b.WriteString("§7Active Queues:\n")
						for srv, count := range summary {
							b.WriteString(fmt.Sprintf("  §f• %s: §e%d player(s)\n", srv, count))
						}
					}
					return cmdCtx.Source.SendMessage(&c.Text{Content: b.String()})
				})),
			)
	}

	p.Command().Register(buildAdminCmd("smartlimbo"))
	p.Command().Register(buildAdminCmd("slimbo"))
}
