# Gate SmartLimbo

[![Go Reference](https://pkg.go.dev/badge/github.com/andreisugu/gate-smartlimbo.svg)](https://pkg.go.dev/github.com/andreisugu/gate-smartlimbo)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

A smart **Limbo failover, per-server auto-reconnect queue, and protected sandbox extension** for the [Gate Minecraft proxy](https://github.com/minekube/gate).

Keeps your players connected, calm, and informed when backend servers restart, crash, or enter maintenance.

---

## ✨ Features

- 🌀 **Seamless Limbo Failover**: When backend servers restart or crash, players are moved to a lightweight Limbo server (e.g. `nanolimbo`, `picolimbo`, `limbo`) instead of being disconnected to the title screen.
- 🚦 **Per-Server Smart Queue**: Players wait specifically for the server they were playing on (e.g., `smp`, `survival`), without getting stuck behind players waiting for different servers.
- 🔄 **Automatic Backend Ping & Reconnect**: Checks backend server TCP ports in the background. As soon as the server boots up, players are automatically reconnected in controlled batches (`reconnect_batch_size`) to prevent server lag spikes.
- 📢 **Live Queue Updates**: Action bar and chat notifications keep players informed about their queue position, total waiting, and server status.
- 🔒 **Protected Limbo Sandbox**: Blocks unauthorized commands (e.g. `/server`, `/msg`, `/pay`) while in Limbo with custom messages and whitelist control.
- 🎮 **Interactive Player Commands**:
  - `/reconnect` (alias `/rc`): Try reconnecting immediately.
  - `/queue leave` (alias `/leave`, `/hub`): Leave the queue and return to the main lobby server.
  - `/queue info`: View your queue status and position.
- 🛠️ **Admin Controls**: `/smartlimbo status` (view all queues in real-time) and `/smartlimbo reload`.

---

## 📦 Installation

### In Pelican / Pterodactyl Panel
Add the plugin package to your **Gate Go Plugins (List)** setting:
```text
github.com/andreisugu/gate-smartlimbo
```
*(Combine with other plugins: `github.com/andreisugu/gate-simplewhitelist, github.com/andreisugu/gate-hybridforwarding, github.com/andreisugu/gate-bettertab, github.com/andreisugu/gate-smartlimbo`)*

### In Go Code
```bash
go get github.com/andreisugu/gate-smartlimbo
```

```go
package main

import (
	"github.com/andreisugu/gate-smartlimbo"
	"go.minekube.com/gate/cmd/gate"
	"go.minekube.com/gate/pkg/edition/java/proxy"
)

func main() {
	proxy.Plugins = append(proxy.Plugins,
		smartlimbo.Plugin,
	)
	gate.Execute()
}
```

---

## ⚙️ Configuration (`config/smartlimbo.toml`)

```toml
# The backend server name used as Limbo (e.g. nanolimbo, picolimbo, limbo)
limbo_server = "nanolimbo"

# Where to send direct connections if target server is down
direct_connect_server = "lobby"

# Fallback servers when a player leaves the queue with /leave
fallback_servers = ["lobby"]

# Queue checks interval in milliseconds
task_interval_ms = 3000

# Queue position notifications interval
queue_notify_interval_ms = 3000

# Where queue notifications are displayed: "action_bar", "chat", "both", or "none"
notify_mode = "action_bar"

# How many players to reconnect per tick
reconnect_batch_size = 3
reconnect_delay_ms = 500
ping_timeout_ms = 1500

# Protected Limbo sandbox
protected_limbo = true
allowed_commands = ["reconnect", "rc", "queue", "leave", "hub", "help", "msg"]

[messages]
kicked_to_limbo = "<yellow>⚠ <aqua>%server%</aqua> went offline or restarted.\n<gray>You were moved to Limbo and placed in queue <gold>#%position%</gold>.</gray></yellow>"
queue_actionbar = "<yellow>⏳ Waiting for <aqua>%server%</aqua> <dark_gray>•</dark_gray> Position: <gold>#%position%</gold>/<gold>%total%</gold></yellow>"
server_online = "<green>✔ <aqua>%server%</aqua> is back online! Reconnecting now...</green>"
reconnect_success = "<green>✔ Reconnected to <aqua>%server%</aqua>!</green>"
reconnect_failed = "<red>✖ <aqua>%server%</aqua> is not ready yet. Still waiting in queue (#%position%)...</red>"
left_queue = "<yellow>You left the queue and returned to <aqua>%server%</aqua>.</yellow>"
not_in_queue = "<gray>You are not currently in any reconnect queue.</gray>"
queue_info = "<aqua>════ SmartLimbo Queue Info ════</aqua>\n<gray>Target Server:</gray> <yellow>%server%</yellow>\n<gray>Your Position:</gray> <gold>#%position%</gold> <gray>of</gray> <gold>%total%</gold>\n<gray>Server Status:</gray> <yellow>%status%</yellow>"
command_blocked = "<red>✖ Commands are blocked in Limbo.\n<gray>Use <yellow>/leave</yellow> to exit or <yellow>/reconnect</yellow> to retry.</gray></red>"
```

---

## 🎮 In-Game Commands

| Command | Permission | Description |
| :--- | :--- | :--- |
| `/reconnect` (or `/rc`) | All | Force an immediate reconnect attempt to your target server. |
| `/queue info` (or `/queue`) | All | Shows your target server, queue position, total waiting, and server status. |
| `/queue leave` (or `/leave`, `/hub`) | All | Leaves the reconnection queue and redirects you to the lobby. |
| `/smartlimbo status` | Op / Console | Displays real-time counts of all active queues across servers. |
| `/smartlimbo reload` | Op / Console | Hot-reloads `config/smartlimbo.toml` with zero downtime. |

---

## 📄 License

[Apache 2.0](LICENSE) © Andrei
