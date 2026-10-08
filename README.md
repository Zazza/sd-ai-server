# SD Studio Server

[Русский](README-ru.md)

Standalone Go service that orchestrates AI components — Stable Diffusion WebUI, Ollama — with installation, lifecycle management, health monitoring, GPU optimization, and a terminal dashboard.

> **Note:** Rembg support was removed in v1.1.0 — recent versions of SD Studio no longer use it.
> The last release with Rembg support is [v0.5.0](https://github.com/Zazza/sd-ai-server/releases/tag/v0.5.0).

## Overview

```
┌─────────────────────────────────────────────────┐
│              SD Studio Server                     │
├──────────┬───────────┬───────────┬──────────────┤
│ Process  │    GPU    │  Health   │     TUI      │
│ Manager  │ Monitor   │ Monitor   │  Dashboard   │
├──────────┴───────────┴───────────┴──────────────┤
│         GPU Proxy (priority queue, VRAM guard)   │
├─────────────────────────────────────────────────┤
│    HTTP API + mDNS discovery + Reverse Proxy     │
└─────────────────────────────────────────────────┘
```

## Quick Start

```bash
# Build
go build -o sd-studio-server .

# First run — interactive setup wizard
./sd-studio-server --data ~/sd-studio-server

# Headless mode (no TUI)
./sd-studio-server --headless

# Custom port
./sd-studio-server --port 9090
```

## Running Modes

| Mode | Command | Description |
|------|---------|-------------|
| TUI (interactive) | `./sd-studio-server` | Terminal dashboard with real-time status |
| Attach (remote) | `./sd-studio-server attach 192.168.1.184` | Connect a TUI dashboard to a running headless daemon; `q` detaches, the daemon keeps running |
| Headless | `./sd-studio-server --headless` | Log output only — for servers and Docker |
| Custom port | `./sd-studio-server --port 9090` | Override HTTP API port |
| Custom config | `./sd-studio-server --config path.yaml` | Use specific config file |

## Configuration

Stored in `{data-dir}/server-config.yaml`. Key fields:

```yaml
port: 8080
data_dir: ~/sd-studio-server
active_sd: forge
mdns: true
```

See [docs/server-en.md](docs/server-en.md) for full configuration reference.

## Core Components

| Component | Package | Description |
|-----------|---------|-------------|
| Process Manager | `process/` | Lifecycle management of AI services as child processes |
| GPU Monitor | `gpu/` | Real-time GPU monitoring via nvidia-smi |
| GPU Queue | `gpuqueue/` | Weighted VRAM budget with FIFO queue, leases, Lease-API |
| GPU Proxy | `gpuproxy/` | Legacy standalone priority proxy with VRAM guard (off by default) |
| Health Monitor | `health/` | Periodic HTTP health checks |
| Installer | `installer/` | Automated component installation |
| mDNS Discovery | `mdns.go` | Service discovery on local network |
| TUI Dashboard | `tui/` | Interactive terminal interface (bubbletea) |

## API Reference

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/server/status` | GET | Status of all managed processes + GPU info |
| `/api/server/start/{name}` | POST | Start a process |
| `/api/server/stop/{name}` | POST | Stop a process |
| `/api/server/restart/{name}` | POST | Restart a process |
| `/api/server/logs/{name}` | GET | Process logs (`?lines=100`) |
| `/api/gpu/status` | GET | GPU queue status (budget, running, queue, warnings) |
| `/api/gpu/lease` | POST | Acquire GPU lease for external workers |
| `/api/models` | GET | List available SD models |
| `/api/models/download` | POST | Download model by URL |
| `/api/models/{name}` | DELETE | Delete model |
| `/api/backends` | GET | List available backends |
| `/api/backends/switch` | POST | Switch active backend |
| `/api/health` | GET | Health check results |
| `/api/sd/*` | * | Proxy → Stable Diffusion WebUI |
| `/api/llm/*` | * | Proxy → Ollama |

## Docker

```bash
docker compose up --build -d
```

## Project Structure

```
.
├── main.go              # Entrypoint (TUI / headless / attach modes)
├── attach/              # Remote TUI client (connects to a daemon over REST)
├── handlers.go          # HTTP API handlers
├── proxy.go             # Reverse proxy handler
├── backends.go          # Backend switching logic
├── mdns.go              # mDNS service discovery
├── config/              # Configuration types and defaults
├── gpu/                 # GPU monitoring via nvidia-smi
├── gpuproxy/            # Legacy standalone priority proxy (off by default)
├── gpuqueue/            # GPU budget queue (weighted semaphore, leases)
├── health/              # HTTP health checks
├── installer/           # Component installation
├── models/              # SD/LLM model management
├── process/             # Process lifecycle management
└── tui/                 # Terminal dashboard (bubbletea)
```

## Documentation

- [Full documentation (English)](docs/server-en.md)
- [Полная документация (Русский)](docs/server-ru.md)
