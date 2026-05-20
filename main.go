package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"sd-studio-server/tui"
)

func main() {
	dataDir := flag.String("data", "", "data directory (default: ~/sd-studio-server)")
	configPath := flag.String("config", "", "config file path (default: {data-dir}/server-config.yaml)")
	port := flag.Int("port", 0, "override server port")
	headless := flag.Bool("headless", false, "run without TUI (log output only)")
	flag.Parse()

	dir := *dataDir
	if dir == "" {
		dir = defaultDataDir()
	}
	dir, _ = filepath.Abs(dir)

	cfgFile := *configPath
	if cfgFile == "" {
		cfgFile = filepath.Join(dir, "server-config.yaml")
	}

	_, configErr := os.Stat(cfgFile)
	firstRun := configErr != nil

	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Fatalf("Failed to create data directory %s: %v", dir, err)
	}

	cfg, err := LoadWithDir(cfgFile, dir)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if *port != 0 {
		cfg.Port = *port
	}

	isTerminal := term.IsTerminal(int(os.Stdin.Fd()))
	useTUI := !*headless && isTerminal

	if useTUI {
		runTUI(cfg, cfgFile, firstRun)
	} else {
		runHeadless(cfg)
	}
}

func runHeadless(cfg *Config) {
	log.Printf("SD Studio Server starting on port %d (backend: %s)", cfg.Port, cfg.ActiveSD)
	log.Printf("Data directory: %s", cfg.DataDir)

	inst := NewInstaller(cfg)
	inst.EnsurePythonBasePackages()
	pm := NewProcessManager(cfg, inst)
	proxy := NewProxyHandler(pm, cfg)
	hm := NewHealthMonitor(cfg)
	gm := NewGPUMonitor()
	mm := NewModelManager(cfg)
	bm := NewBackendManager(cfg)
	handlers := NewHandlers(pm, hm, gm, cfg, inst)

	mux := setupMux(handlers, inst, mm, bm, proxy)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	inst.EnsureAllInstalled()
	ensureOllamaBinary(cfg, inst)
	pm.StartAll()

	go pm.Watch(ctx)
	go hm.Start(ctx)
	go gm.Start(ctx)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      corsMiddleware(mux),
		ReadTimeout:  0,
		WriteTimeout: 0,
	}

	mdns := NewMDNS(cfg.Port, cfg.MDNS)
	if err := mdns.Register(); err != nil {
		log.Printf("mDNS registration failed (non-fatal): %v", err)
	} else if cfg.MDNS {
		log.Printf("mDNS: registered _sd-studio._tcp on port %d", cfg.Port)
	}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		cancel()
		srv.Shutdown(shutdownCtx)
		mdns.Shutdown()
		pm.StopAll()

		log.Println("Server stopped")
		os.Exit(0)
	}()

	log.Printf("HTTP server listening on :%d", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

func runTUI(cfg *Config, cfgFile string, firstRun bool) {
	logCapture := NewLogCapture(300)
	log.SetOutput(logCapture)

	if firstRun {
		result := tui.RunWizard(cfg.DataDir)
		if result.DataDir == "" {
			return
		}

		if err := os.MkdirAll(result.DataDir, 0755); err != nil {
			log.Fatalf("Failed to create data directory: %v", err)
		}

		freshCfg := newDefaultConfig()
		freshCfg.DataDir = result.DataDir

		for key, active := range result.Components {
			if !active {
				if proc, ok := freshCfg.Processes[key]; ok {
					proc.AutoStart = false
					proc.Install = InstallConfig{}
					freshCfg.Processes[key] = proc
				}
			}
		}

		freshCfg.applyBackendToProcess()
		freshCfg.applyInstallDefaults()
		freshCfg.resolvePaths()

		if err := saveConfig(cfgFile, &freshCfg); err != nil {
			log.Fatalf("Failed to save config: %v", err)
		}

		*cfg = freshCfg
	}

	inst := NewInstaller(cfg)
	inst.EnsurePythonBasePackages()
	pm := NewProcessManager(cfg, inst)
	proxy := NewProxyHandler(pm, cfg)
	hm := NewHealthMonitor(cfg)
	gm := NewGPUMonitor()
	mm := NewModelManager(cfg)
	bm := NewBackendManager(cfg)
	handlers := NewHandlers(pm, hm, gm, cfg, inst)

	mux := setupMux(handlers, inst, mm, bm, proxy)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	deps := tui.ServerDeps{
		Port:         cfg.Port,
		DataDir:      cfg.DataDir,
		EnsureAllInstalled: func() {
			inst.EnsureAllInstalled()
			ensureOllamaBinary(cfg, inst)
		},
		InstallStatus: func() map[string]tui.ComponentInstallStatus {
			statuses := inst.Status()
			result := make(map[string]tui.ComponentInstallStatus, len(statuses))
			for k, s := range statuses {
				result[k] = tui.ComponentInstallStatus{
					Key:        s.Key,
					Installed:  s.Installed,
					Installing: s.Installing,
					Progress:   s.Progress,
					Error:      s.Error,
				}
			}
			return result
		},
		StartAll: func() {
			pm.StartAll()
		},
		StartMonitors: func() {
			go pm.Watch(ctx)
			go hm.Start(ctx)
			go gm.Start(ctx)
		},
		ProcStatus: func() map[string]tui.ServiceInfo {
			statuses := pm.Status()
			healthResults := hm.Results()
			result := make(map[string]tui.ServiceInfo, len(statuses))
			for k, ps := range statuses {
				si := tui.ServiceInfo{
					Name:   ps.Name,
					Status: ps.Status,
					PID:    ps.PID,
					Uptime: ps.Uptime,
				}
				if hr, ok := healthResults[k]; ok {
					si.Healthy = hr.Healthy
					si.Latency = hr.LatencyMs
				}
				result[k] = si
			}
			return result
		},
		StartProc: func(name string) error {
			return pm.Start(name)
		},
		StopProc: func(name string) error {
			return pm.Stop(name)
		},
		RestartProc: func(name string) error {
			return pm.Restart(name)
		},
		ProcLogs: func(name string, lines int) []string {
			logs, err := pm.Logs(name, lines)
			if err != nil {
				return nil
			}
			return logs
		},
		GPUInfo: func() tui.GPUInfo {
			info := gm.Info()
			return tui.GPUInfo{
				Name:        info.Name,
				MemoryTotal: info.MemoryTotal,
				MemoryUsed:  info.MemoryUsed,
				Utilization: info.Utilization,
				Available:   info.Available,
			}
		},
		HealthResults: func() map[string]tui.HealthResult {
			results := hm.Results()
			out := make(map[string]tui.HealthResult, len(results))
			for k, v := range results {
				out[k] = tui.HealthResult{
					Healthy:   v.Healthy,
					LatencyMs: v.LatencyMs,
					Error:     v.Error,
				}
			}
			return out
		},
		ServerLogs: func() []string {
				return logCapture.Lines(100)
			},
	}

	model := tui.NewAppModel(deps)
	p := tea.NewProgram(model, tea.WithAltScreen())

	inst.OnProgress = func(key, progress string) {
		p.Send(tui.InstallProgressMsg(key, progress))
	}

	pm.OnChange = func() {
		p.Send(tui.ServicesChangeMsg{})
	}

	gm.OnUpdate = func(info GPUInfo) {
		p.Send(tui.GPUUpdateMsgFunc(tui.GPUInfo{
			Name:        info.Name,
			MemoryTotal: info.MemoryTotal,
			MemoryUsed:  info.MemoryUsed,
			Utilization: info.Utilization,
			Available:   info.Available,
		}))
	}

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      corsMiddleware(mux),
		ReadTimeout:  0,
		WriteTimeout: 0,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		srv.Shutdown(shutdownCtx)
		pm.StopAll()
	}()

	mdns := NewMDNS(cfg.Port, cfg.MDNS)
	if err := mdns.Register(); err != nil {
		log.Printf("mDNS registration failed (non-fatal): %v", err)
	}

	if _, err := p.Run(); err != nil {
		log.Fatalf("TUI error: %v", err)
	}

	cancel()
	pm.StopAll()
	mdns.Shutdown()
}

func saveConfig(path string, cfg *Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	header := []byte("# SD Studio Server Configuration\n\n")
	return os.WriteFile(path, append(header, data...), 0644)
}

func setupMux(handlers *Handlers, inst *Installer, mm *ModelManager, bm *BackendManager, proxy *ProxyHandler) *http.ServeMux {
	mux := http.NewServeMux()
	handlers.RegisterRoutes(mux)
	mm.RegisterRoutes(mux)
	mm.RegisterDeleteRoutes(mux)
	bm.RegisterRoutes(mux)
	inst.RegisterRoutes(mux)

	mux.Handle("/api/sd/", proxy)
	mux.Handle("/api/llm/", proxy)
	mux.Handle("/api/rembg/", proxy)

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			writeJSON(w, map[string]interface{}{
				"name":    "SD Studio Server",
				"version": "1.0",
				"status":  "running",
			})
			return
		}
		http.NotFound(w, r)
	})

	return mux
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func ensureOllamaBinary(cfg *Config, inst *Installer) {
	if ollamaPath := inst.EnsureOllama(); ollamaPath != "" {
		pm := &ProcessConfig{}
		for key, pc := range cfg.Processes {
			if key == "ollama" {
				pm = &pc
				break
			}
		}
		if pm != nil {
			pm.Binary = ollamaPath
			cfg.Processes["ollama"] = *pm
		}
	}
}
