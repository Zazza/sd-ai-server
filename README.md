# SD Studio Server

[Русский](README-ru.md)

Standalone Go service that orchestrates AI components — Stable Diffusion WebUI, Ollama, Rembg — with installation, lifecycle management, health monitoring, GPU optimization, and a terminal dashboard.

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
| GPU Proxy | `gpuproxy/` | Priority-based reverse proxy with VRAM guard |
| Health Monitor | `health/` | Periodic HTTP health checks |
| Installer | `installer/` | Automated component installation |
| mDNS Discovery | `mdns.go` | Service discovery on local network |
| TUI Dashboard | `tui/` | Interactive terminal interface (bubbletea) |

## API Reference

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/api/server/status` | GET | Status of all managed processes |
| `/api/server/start/{name}` | POST | Start a process |
| `/api/server/stop/{name}` | POST | Stop a process |
| `/api/server/restart/{name}` | POST | Restart a process |
| `/api/server/logs/{name}` | GET | Process logs (`?lines=100`) |
| `/api/gpu` | GET | GPU info (name, memory, utilization) |
| `/api/models` | GET | List available SD models |
| `/api/models/download` | POST | Download model by URL |
| `/api/models/{name}` | DELETE | Delete model |
| `/api/backends` | GET | List available backends |
| `/api/backends/switch` | POST | Switch active backend |
| `/api/health` | GET | Health check results |
| `/api/sd/*` | * | Proxy → Stable Diffusion WebUI |
| `/api/llm/*` | * | Proxy → Ollama |
| `/api/rembg/*` | * | Proxy → Rembg |

## Docker

```bash
docker compose up --build -d
```

## Project Structure

```
.
├── main.go              # Entrypoint (TUI / headless modes)
├── handlers.go          # HTTP API handlers
├── proxy.go             # Reverse proxy handler
├── backends.go          # Backend switching logic
├── mdns.go              # mDNS service discovery
├── config/              # Configuration types and defaults
├── gpu/                 # GPU monitoring via nvidia-smi
├── gpuproxy/            # Priority proxy with VRAM guard
├── health/              # HTTP health checks
├── installer/           # Component installation
├── models/              # SD/LLM model management
├── process/             # Process lifecycle management
└── tui/                 # Terminal dashboard (bubbletea)
```

## Documentation

- [Full documentation (English)](docs/server-en.md)
- [Полная документация (Русский)](docs/server-ru.md)
