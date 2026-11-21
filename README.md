<div align="center">

# 🌀 Gate SmartLimbo

**Smart Limbo failover, per-server batched auto-reconnect queues & protected sandbox for Minekube Gate.**

<p>
  <a href="https://github.com/andreisugu/gate-smartlimbo/actions/workflows/ci.yml"><img src="https://github.com/andreisugu/gate-smartlimbo/actions/workflows/ci.yml/badge.svg" alt="CI" /></a>
  <a href="https://pkg.go.dev/github.com/andreisugu/gate-smartlimbo"><img src="https://pkg.go.dev/badge/github.com/andreisugu/gate-smartlimbo.svg" alt="Go Reference" /></a>
  <img src="https://img.shields.io/badge/Go-1.26-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go 1.26" />
  <a href="https://github.com/minekube/gate"><img src="https://img.shields.io/badge/Platform-Minekube_Gate-7c3aed?style=flat-square" alt="Minekube Gate" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-Apache_2.0-blue.svg?style=flat-square" alt="Apache 2.0" /></a>
</p>

<p>
  <a href="#-how-it-works">How It Works</a> •
  <a href="#-installation">Installation</a> •
  <a href="#-configuration">Configuration</a> •
  <a href="#-commands--permissions">Commands</a> •
  <a href="#-minekube-gate-extensions-suite">Gate Suite</a>
</p>

</div>

---

## 🔄 How It Works

When a backend server crashes, restarts, or rejects an initial join, **Gate SmartLimbo** catches the kick on the proxy and keeps players connected smoothly inside Limbo until their server recovers:

```text
Backend Crash / Restart ─► Move to Limbo ─► Per-Server Queue (e.g. survival, smp)
                                                     │
                                        TCP health ping succeeds
                                                     ▼
                          Reconnect in controlled batches (reconnect_batch_size)
```

- **Per-Server Isolated Queues:** Players queued for `survival` aren't stalled behind `creative` restarts, and unreachable fallback servers are safely skipped on `/leave`.
- **Staggered Batch Reconnect:** Reconnects players in rate-controlled batches (`reconnect_batch_size` / `reconnect_delay_ms`) so recovered backends don't get thunder-herded.
- **Live Queue Feedback:** Configurable `action_bar`, `chat`, `both`, or `none` MiniMessage updates.
- **Protected Limbo Sandbox:** Blocks unauthorized and namespaced commands (`/minecraft:*`) while players sit in Limbo unless listed in `allowed_commands`.
- **Smart Kick & Switch Handling:** Ignores bans/whitelist/version kicks, keeps players in their current server when a transfer fails, fails over offline initial servers, caches health probes, and preserves queue order across retries.

---

## 📦 Installation

### Using `gate-plugin-builder` / Pelican Egg
Add to your `plugins.txt` or `GATE_PLUGINS`:
```text
github.com/andreisugu/gate-smartlimbo
```

### In Go Code
```bash
go get github.com/andreisugu/gate-smartlimbo
```

```go
package main

import (
	"go.minekube.com/gate/cmd/gate"
	"go.minekube.com/gate/pkg/edition/java/proxy"

	smartlimbo "github.com/andreisugu/gate-smartlimbo"
)

func main() {
	proxy.Plugins = append(proxy.Plugins, smartlimbo.Plugin)
	gate.Execute()
}
```

---

## ⚙️ Configuration

Generated at `config/smartlimbo.toml`:

```toml
limbo_server = "nanolimbo"
direct_connect_server = "lobby"
fallback_servers = ["lobby"]

task_interval_ms = 3000
queue_notify_interval_ms = 3000
notify_mode = "action_bar" # "action_bar", "chat", "both", or "none"
reconnect_batch_size = 3
reconnect_delay_ms = 500
ping_timeout_ms = 1500

protected_limbo = true
allowed_commands = ["reconnect", "rc", "queue", "leave", "hub", "help", "msg"]
```

---

## ⌨️ Commands & Permissions

| Command | Permission | Description |
| :--- | :--- | :--- |
| `/reconnect` *(or `/rc`)* | All | Attempt an immediate reconnect to your queued server |
| `/queue info` *(or `/queue`)* | All | Show queued backend name, queue position, and server status |
| `/hub` *(or `/lobby`)* | All | Transfer to the primary lobby / playable fallback server anytime |
| `/queue leave` *(or `/leave`)* | All | Leave the reconnect queue (and exit Limbo to a fallback server) |
| `/smartlimbo status` *(or `/slimbo status`)* | `smartlimbo.status` *(or `smartlimbo.admin`)* | Show Limbo target and per-server queue sizes |
| `/smartlimbo reload` *(or `/slimbo reload`)* | `smartlimbo.reload` *(or `smartlimbo.admin`)* | Hot-reload `config/smartlimbo.toml` without downtime |

---

## 🌐 Minekube Gate Extensions Suite

| Project | Role | Stack |
| :--- | :--- | :--- |
| [`gate-stonyperms`](https://github.com/andreisugu/gate-stonyperms) | LuckPerms-style permissions, contexts, tracks & v0.2 R/W HTTP + SSE API | `Go 1.26` |
| [`stonyperms-bridge`](https://github.com/andreisugu/stonyperms-bridge) | Paper & Fabric permission adapters, live SSE, `/spb` commands, Vault & PAPI | `Java 25` |
| [`gate-bettertab`](https://github.com/andreisugu/gate-bettertab) | Animated RGB / MiniMessage TAB lists, server groups, weights & placeholders | `Go 1.26` |
| [`gate-discord4gate`](https://github.com/andreisugu/gate-discord4gate) | Bidirectional Discord <> Minecraft chat, slash commands & webhook avatars | `Go 1.26` |
| [`gate-hybridforwarding`](https://github.com/andreisugu/gate-hybridforwarding) | Per-backend Velocity Modern, Legacy BungeeCord, BungeeGuard & None forwarding | `Go 1.26` |
| [`gate-simplewhitelist`](https://github.com/andreisugu/gate-simplewhitelist) | Lock-free `atomic.Pointer` whitelist by username, UUID, IP & CIDR | `Go 1.26` |
| [`gate-skinschanger`](https://github.com/andreisugu/gate-skinschanger) | Resilient multi-provider & Mineskin URL skin switcher with live backend refresh | `Go 1.26` |
| **[`gate-smartlimbo`](https://github.com/andreisugu/gate-smartlimbo)** *(this repo)* | Limbo failover, protected sandbox & batched backend auto-reconnect queues | `Go 1.26` |
| [`gate-plugin-builder`](https://github.com/andreisugu/gate-plugin-builder) | Zero-config static Gate bundler (`build.sh`) + Pelican / Pterodactyl egg | `Shell` |

---

## 📄 License

Licensed under the [Apache License 2.0](LICENSE) © Andrei.
