# Changelog

## [Unreleased]

## [1.1.0] — 2026-10-08

### Added
- Attach mode: `sd-studio-server attach [host[:port]]` — remote TUI dashboard over the daemon REST API (3s polling, reconnect banner with backoff, process control, logs view); `q` exits the client only, the daemon keeps running; daemon strings sanitized against terminal ESC injection, 2 MB response cap
- `/api/server/status` now includes a `sys` block (daemon host CPU/RAM) for remote dashboards
- GPU budget queue (`gpuqueue/`): weighted VRAM semaphore with FIFO queue + priority, TTL leases with heartbeat, fail-open semantics, bounded wait → 503 + Retry-After
- Proxy gating: heavy SD/LLM paths acquire GPU budget before forwarding; SD weight = checkpoint file size + overhead (checkpoint tracked by sniffing `POST /sdapi/v1/options`)
- Lease API for external GPU consumers (yue-worker): `POST /api/gpu/lease`, heartbeat, `DELETE`, `PATCH /api/gpu/queue/{id}`, `GET /api/gpu/status`
- Embedded web panel at `/ui` (budget bar, running jobs, queue move/cancel, warnings; 2s polling)
- TUI: GPU queue line (budget bar, run/queue counts, warnings)
- Config section `gpu:` (auto budget from detected VRAM, weights, TTL, max wait)

### Changed
- `gpuproxy/` marked legacy (standalone mode, off by default) — superseded by `gpuqueue`
- CORS middleware now allows PATCH (queue reorder)

### Removed
- Web panel `/ui` (package `webui`); GPU queue management moved to the TUI (`g` screen, local + attach)
- Rembg component: process config, `/api/rembg/` proxy route, installer step, wizard entry, docs (client dropped it in v0.8.0)

## [1.0.0] — 2025-05-30

### Added
- REST API server for managing AI services (SD WebUI Forge, Ollama, Rembg)
- Process lifecycle management (start, stop, restart, watch, auto-restart)
- Health monitoring with HTTP checks and latency tracking
- GPU monitor — VRAM tracking, utilization, auto-configure Ollama based on VRAM
- GPU proxy with priority queue and VRAM guard for concurrent inference
- Component installer — SD WebUI Forge, Ollama, Rembg with Python venv
- Model manager — download, list, delete SD models
- mDNS service discovery for LAN clients
- Bubble Tea TUI dashboard with setup wizard
- Docker support (multi-stage Alpine build)
- GitHub Actions CI (test + vet on push/PR)
- GitHub Actions Release (cross-platform binaries on tag)

### Fixed
- Run Ollama on CPU when VRAM < 16GB to free memory for SD generation
- Auto-configure Ollama keep_alive based on available VRAM
- Bind Ollama to 0.0.0.0 for external access on LAN
- Set LD_LIBRARY_PATH for Ollama to find CUDA libraries
- Preserve Ollama Args/WorkDir when updating binary path after install
- Only stop managed processes on shutdown, not system services
- Download progress for Ollama installer in TUI
- Generation stability, compound presets, LLM JSON mode, UI fixes
