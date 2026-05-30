[English](server-en.md) | [Русский](server-ru.md) | [Back to README](../README.md)

# SD Studio Server

Standalone Go service that orchestrates all AI components — Stable Diffusion WebUI, Ollama, Rembg — with installation, lifecycle management, health monitoring, GPU optimization, and a terminal dashboard.

## Overview

SD Studio Server eliminates manual setup of AI services. It installs, starts, monitors and manages all components through a single configuration file, accessible via HTTP API or interactive TUI.

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

## Installation

### Build from source

```bash
go build -o sd-studio-server .
```

### Docker

```bash
docker compose up --build
```

## Quick Start

```bash
# First run — interactive setup wizard
./sd-studio-server --data ~/sd-studio-server

# Headless mode (no TUI)
./sd-studio-server --headless

# Custom port
./sd-studio-server --port 9090

# Custom config
./sd-studio-server --config /path/to/server-config.yaml
```

On first run, the setup wizard guides you through:
- Selecting which components to install (SD WebUI, Ollama, Rembg)
- Choosing data directory
- Configuring GPU backend (Forge / A1111)

## Running Modes

| Mode | Command | Description |
|------|---------|-------------|
| TUI (interactive) | `./sd-studio-server` | Terminal dashboard with real-time service status and controls |
| Headless | `./sd-studio-server --headless` | Log output only, no TUI — for servers and Docker |
| Custom port | `./sd-studio-server --port 9090` | Override HTTP API port |
| Custom config | `./sd-studio-server --config path.yaml` | Use specific config file |

## Configuration

Configuration is stored in `{data-dir}/server-config.yaml`:

```yaml
port: 8080
data_dir: ~/sd-studio-server
active_sd: forge
mdns: true
detected_vram_mb: 8192

processes:
  python:
    name: "Python 3.10"
    autostart: false
    category: utility

  sd:
    name: "Stable Diffusion"
    health_url: "http://localhost:7860/sdapi/v1/options"
    target_url: "http://localhost:7860"
    proxy_path: "/api/sd/"
    autostart: true
    restart: true
    max_restart: 5
    env:
      PYTORCH_CUDA_ALLOC_CONF: "expandable_segments:True"

  ollama:
    name: "Ollama"
    binary: "ollama"
    args: ["serve"]
    health_url: "http://localhost:11434/api/tags"
    target_url: "http://localhost:11434"
    proxy_path: "/api/llm/"
    autostart: true
    restart: true
    max_restart: 5

  rembg:
    name: "Rembg"
    binary: "rembg"
    args: ["s", "--host", "0.0.0.0", "--port", "7000"]
    health_url: "http://localhost:7000/api"
    target_url: "http://localhost:7000"
    proxy_path: "/api/rembg/"
    autostart: false
    restart: true
    max_restart: 3
    category: utility

backends:
  forge:
    name: "Stable Diffusion Forge"
    process_key: "sd"
    binary: "python/bin/python3"
    args: ["launch.py", "--listen", "--api", "--xformers", "--medvram-sdxl"]
    auto_optimize: true
    workdir: "stable-diffusion-webui-forge"
    models_dir: "stable-diffusion-webui-forge/models/Stable-diffusion"
    lora_dir: "stable-diffusion-webui-forge/models/Lora"
    vae_dir: "stable-diffusion-webui-forge/models/VAE"
    embedding_dir: "stable-diffusion-webui-forge/embeddings"
    install:
      method: zip
      url: "https://github.com/lllyasviel/stable-diffusion-webui-forge/archive/refs/heads/main.zip"
      target: "stable-diffusion-webui-forge"
      version: main

proxy:
  enabled: true
  gpu_slots: 1
  studio_header: "X-SD-Studio"
  endpoints:
    sd:
      listen_addr: ":7860"
      target_url: "http://localhost:7860"
    ollama:
      listen_addr: ":11434"
      target_url: "http://localhost:11434"
```

### Process Configuration

| Field | Description |
|-------|-------------|
| `name` | Display name |
| `binary` | Executable path (relative to data dir or absolute) |
| `args` | Command-line arguments |
| `env` | Environment variables |
| `workdir` | Working directory |
| `health_url` | HTTP endpoint for health checks |
| `target_url` | Target URL for reverse proxy |
| `proxy_path` | API prefix for proxy routing |
| `autostart` | Start automatically on server launch |
| `restart` | Auto-restart on failure |
| `max_restart` | Maximum restart attempts |
| `category` | Group category (empty = main, `utility` = auxiliary) |

### Install Configuration

| Field | Description |
|-------|-------------|
| `method` | Installation method: `zip`, `binary`, `pip`, `archive`, `tgz` |
| `url` | Download URL |
| `target` | Target directory or package name |
| `version` | Version tag |

### Backend Configuration

| Field | Description |
|-------|-------------|
| `name` | Display name |
| `process_key` | Reference to process in `processes` map |
| `binary` | Binary override for this backend |
| `args` | Launch arguments |
| `workdir` | Working directory |
| `models_dir` | SD checkpoints directory |
| `lora_dir` | LoRA models directory |
| `vae_dir` | VAE models directory |
| `embedding_dir` | Textual inversion embeddings directory |
| `auto_optimize` | Auto-adjust flags based on detected VRAM |

## Core Components

### Process Manager (`process/`)

Manages lifecycle of AI services as child processes.

- Start, stop, restart processes via API or TUI
- Auto-restart on failure (configurable max retries)
- Log capture and retrieval
- Process status tracking (starting, running, stopped, failed)
- Graceful shutdown with signal forwarding

### GPU Monitor (`gpu/`)

Real-time GPU monitoring via nvidia-smi.

- Polls every 5 seconds
- Tracks: GPU name, memory total/used/free, utilization %
- Provides `OptimizerAdapter` for auto-optimizing SD launch flags based on VRAM:
  - `--medvram-sdxl` for 8GB GPUs
  - `--medvram` for 6GB GPUs
  - `--lowvram` for 4GB GPUs
- Falls back gracefully when nvidia-smi is unavailable

### GPU Proxy (`gpuproxy/`)

Priority-based reverse proxy with GPU slot management.

- **Priority queue** — requests with `X-SD-Studio` header get high priority
- **GPU slot limiting** — configurable number of concurrent GPU jobs
- **VRAM cooldown** — after each job completes, waits for >= 50% free VRAM before dispatching next job (prevents OOM on low-VRAM GPUs)
- **Timeout** — 30 second fallback to avoid permanent blocking
- **Multiple endpoints** — separate proxy ports for SD and Ollama

```
Client → Proxy (:7860) → Queue → Slot → SD WebUI
Client → Proxy (:11434) → Queue → Slot → Ollama
                   ↑
         Priority + VRAM Guard
```

### Health Monitor (`health/`)

Periodic HTTP health checks for all configured services.

- Configurable check interval
- Latency measurement
- Status tracking (healthy/unhealthy)
- Results available via API and TUI

### Installer (`installer/`)

Automated installation of all components.

- Downloads and extracts SD WebUI Forge
- Installs Python 3.10 standalone (platform-specific)
- Installs Rembg via pip
- Ensures Ollama binary is available
- Progress reporting via callbacks
- Installation status tracking

### mDNS Discovery (`mdns.go`)

Service discovery on local network.

- Advertises `_sd-studio._tcp` service
- Desktop apps can auto-discover the server
- Configurable via `mdns: true/false` in config

### TUI Dashboard (`tui/`)

Interactive terminal interface built with bubbletea.

- Service status overview with health indicators
- Start/stop/restart controls
- GPU info panel (memory, utilization)
- Log viewer with scrolling
- Installation progress tracking
- First-run setup wizard

## API Reference

### Server Status

```
GET /api/server/status
```

Returns status of all managed processes:

```json
{
  "processes": {
    "sd": { "name": "Stable Diffusion", "status": "running", "pid": 12345, "uptime": "2h30m" },
    "ollama": { "name": "Ollama", "status": "running", "pid": 12340, "uptime": "2h30m" },
    "rembg": { "name": "Rembg", "status": "stopped" }
  }
}
```

### Process Control

```
POST /api/server/start/{name}
POST /api/server/stop/{name}
POST /api/server/restart/{name}
GET  /api/server/logs/{name}?lines=100
```

### GPU Info

```
GET /api/gpu
```

```json
{
  "name": "NVIDIA GeForce RTX 3060",
  "memory_total_mb": 12288,
  "memory_used_mb": 4096,
  "memory_free_mb": 8192,
  "utilization_percent": 45,
  "available": true
}
```

### Models

```
GET  /api/models                # List available SD models
POST /api/models/download       # Download model by URL
DELETE /api/models/{name}       # Delete model
```

### Backends

```
GET  /api/backends              # List available backends
POST /api/backends/switch       # Switch active backend
```

### Health

```
GET /api/health                 # Health check results for all services
```

### Proxy Routes

```
/api/sd/*    → Stable Diffusion WebUI
/api/llm/*   → Ollama / LLM service
/api/rembg/* → Rembg service
```

## Docker Deployment

```yaml
# docker-compose.yml
services:
  sd-studio-server:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - ./data:/root/sd-studio-server
    deploy:
      resources:
        reservations:
          devices:
            - driver: nvidia
              count: 1
              capabilities: [gpu]
```

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
├── config/
│   ├── types.go         # Config, ProcessConfig, BackendConfig
│   ├── defaults.go      # Default configuration values
│   └── resolve.go       # Path resolution
├── gpu/
│   └── gpu.go           # GPU monitoring via nvidia-smi
├── gpuproxy/
│   ├── config.go        # Proxy configuration
│   ├── proxy.go         # Proxy with VRAM cooldown
│   ├── handler.go       # Reverse proxy handler per endpoint
│   └── queue.go         # Priority queue with slot management
├── health/
│   └── monitor.go       # HTTP health checks
├── installer/
│   └── installer.go     # Component installation
├── models/
│   └── manager.go       # SD/LLM model management
├── process/
│   ├── manager.go       # Process lifecycle management
│   └── types.go         # Process status types
└── tui/
    ├── model.go         # bubbletea root model
    ├── dashboard.go     # Main dashboard view
    ├── wizard.go        # First-run setup wizard
    └── styles.go        # TUI styling
```
