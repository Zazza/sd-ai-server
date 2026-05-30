# Changelog

## [Unreleased]

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
