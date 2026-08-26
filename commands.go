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

// RegisterCommands registers /reconnect, /rc, /queue, /leave, and /smartlimbo commands.
func RegisterCommands(p *proxy.Proxy, store *ConfigStore, queue *QueueManager) {
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

		_ = player.SendMessage(&c.Text{Content: "§e[SmartLimbo] Attempting immediate reconnection..."})

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			res, err := player.CreateConnectionRequest(targetServer).Connect(ctx)
			if err == nil && res.Status() == proxy.SuccessConnectionStatus {
				queue.Remove(player.ID())
				msg := strings.ReplaceAll(cfg.Messages.ReconnectSuccess, "%server%", targetServer.ServerInfo().Name())
				_ = player.SendMessage(FormatText(msg))
			} else {
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
	// 2. /queue and /queue leave / info
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

		_, queued := queue.Remove(player.ID())
		if !queued {
			return player.SendMessage(FormatText(cfg.Messages.NotInQueue))
		}

		// Redirect player to first available fallback server
		var fallback proxy.RegisteredServer
		for _, name := range cfg.FallbackServers {
			if srv := p.Server(name); srv != nil {
				fallback = srv
				break
			}
		}

		if fallback != nil {
			go func() {
				_, _ = player.CreateConnectionRequest(fallback).Connect(context.Background())
			}()
			msg := strings.ReplaceAll(cfg.Messages.LeftQueue, "%server%", fallback.ServerInfo().Name())
			return player.SendMessage(FormatText(msg))
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
		if targetSrv := p.Server(targetServerName); targetSrv != nil {
			if isServerReachable(targetSrv.ServerInfo().Addr().String(), 1000*time.Millisecond) {
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

	// Direct aliases /leave and /hub
	p.Command().Register(brigodier.Literal("leave").Executes(command.Command(leaveQueueFunc)))
	p.Command().Register(brigodier.Literal("hub").Executes(command.Command(leaveQueueFunc)))

	// -------------------------------------------------------------------------
	// 3. /smartlimbo admin command
	// -------------------------------------------------------------------------
	p.Command().Register(
		brigodier.Literal("smartlimbo").
			Executes(command.Command(func(cmdCtx *command.Context) error {
				return cmdCtx.Source.SendMessage(&c.Text{
					Content: "§b[SmartLimbo for Gate] §7by Andrei\n" +
						"§7Commands: §f/smartlimbo reload§7, §f/smartlimbo status§7, §f/reconnect§7, §f/queue info§7, §f/queue leave",
				})
			})).
			Then(brigodier.Literal("reload").
				Executes(command.Command(func(cmdCtx *command.Context) error {
					if _, err := store.Load(); err != nil {
						return cmdCtx.Source.SendMessage(&c.Text{Content: fmt.Sprintf("§c[SmartLimbo] Reload failed: %v", err)})
					}
					return cmdCtx.Source.SendMessage(&c.Text{Content: "§a[SmartLimbo] Configuration reloaded successfully!"})
				})),
			).
			Then(brigodier.Literal("status").
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
			),
	)
}
