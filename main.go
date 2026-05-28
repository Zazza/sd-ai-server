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

	"sd-studio-server/api"
	"sd-studio-server/config"
	sdgpu "sd-studio-server/gpu"
	"sd-studio-server/gpuproxy"
	sdhealth "sd-studio-server/health"
	"sd-studio-server/installer"
	"sd-studio-server/models"
	"sd-studio-server/process"
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
		dir = config.DefaultDataDir()
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

	cfg, err := config.LoadWithDir(cfgFile, dir)
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

type appDeps struct {
	cfg      *config.Config
	inst     *installer.Installer
	pm       *process.ProcessManager
	hm       *sdhealth.HealthMonitor
	gm       *sdgpu.GPUMonitor
	gpuProxy *gpuproxy.Proxy
	mux      *http.ServeMux
}

type gpuMonitorAdapter struct {
	*sdgpu.GPUMonitor
}

func (a *gpuMonitorAdapter) Info() gpuproxy.GPUInfo {
	gi := a.GPUMonitor.Info()
	return gpuproxy.GPUInfo{
		Name:        gi.Name,
		MemoryTotal: gi.MemoryTotal,
		MemoryFree:  gi.MemoryFree,
		MemoryUsed:  gi.MemoryUsed,
		Utilization: gi.Utilization,
		Available:   gi.Available,
	}
}

func initApp(cfg *config.Config) *appDeps {
	gpuOpt := sdgpu.NewOptimizerAdapter()
	cfg.ApplyBackendToProcess(gpuOpt)
	cfg.ApplyProxyPorts(log.Printf)

	inst := installer.NewInstaller(cfg)
	inst.EnsurePythonBasePackages()
	pm := process.NewProcessManager(cfg, inst, cfg.DataDir)
	proxy := NewProxyHandler(pm, cfg)
	hm := sdhealth.NewHealthMonitor(cfg)
	gm := sdgpu.NewGPUMonitor()
	mm := models.NewModelManager(cfg)
	bm := NewBackendManager(cfg)
	handlers := NewHandlers(pm, hm, gm, cfg, inst)
	mux := setupMux(handlers, inst, mm, bm, proxy)

	var gpuProxy *gpuproxy.Proxy
	if cfg.Proxy.Enabled {
		gpuProxy = gpuproxy.New(cfg.Proxy, &gpuMonitorAdapter{gm})
		if err := gpuProxy.Start(); err != nil {
			log.Fatalf("GPU proxy start failed: %v", err)
		}
	}

	return &appDeps{
		cfg: cfg, inst: inst, pm: pm, hm: hm, gm: gm,
		gpuProxy: gpuProxy, mux: mux,
	}
}

func (d *appDeps) startMonitors(ctx context.Context) {
	go d.pm.Watch(ctx)
	go d.hm.Start(ctx)
	go d.gm.Start(ctx)
}

func (d *appDeps) ensureInstalled() {
	d.inst.EnsureAllInstalled()
	ensureOllamaBinary(d.cfg, d.pm, d.inst)
}

func (d *appDeps) newServer() *http.Server {
	return &http.Server{
		Addr:         fmt.Sprintf(":%d", d.cfg.Port),
		Handler:      corsMiddleware(d.mux),
		ReadTimeout:  0,
		WriteTimeout: 0,
	}
}

func (d *appDeps) registerMDNS() *MDNSService {
	mdns := NewMDNS(d.cfg.Port, d.cfg.MDNS)
	if err := mdns.Register(); err != nil {
		log.Printf("mDNS registration failed (non-fatal): %v", err)
	} else if d.cfg.MDNS {
		log.Printf("mDNS: registered _sd-studio._tcp on port %d", d.cfg.Port)
	}
	return mdns
}

func (d *appDeps) shutdown() {
	if d.gpuProxy != nil {
		d.gpuProxy.Stop()
	}
	d.pm.StopAll()
}

func runHeadless(cfg *config.Config) {
	log.Printf("SD Studio Server starting on port %d (backend: %s)", cfg.Port, cfg.ActiveSD)
	log.Printf("Data directory: %s", cfg.DataDir)

	d := initApp(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	d.ensureInstalled()
	d.pm.StartAll()
	d.startMonitors(ctx)

	srv := d.newServer()
	mdns := d.registerMDNS()

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		cancel()
		srv.Shutdown(shutdownCtx)
		d.shutdown()
		mdns.Shutdown()

		log.Println("Server stopped")
		os.Exit(0)
	}()

	log.Printf("HTTP server listening on :%d", cfg.Port)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
}

func runTUI(cfg *config.Config, cfgFile string, firstRun bool) {
	logCapture := process.NewLogCapture(300)
	log.SetOutput(logCapture)

	if firstRun {
		result := tui.RunWizard(cfg.DataDir)
		if result.DataDir == "" {
			return
		}

		if err := os.MkdirAll(result.DataDir, 0755); err != nil {
			log.Fatalf("Failed to create data directory: %v", err)
		}

		freshCfg := config.NewDefault()
		freshCfg.DataDir = result.DataDir

		for key, active := range result.Components {
			if !active {
				if proc, ok := freshCfg.Processes[key]; ok {
					proc.AutoStart = false
					proc.Install = config.InstallConfig{}
					freshCfg.Processes[key] = proc
				}
			}
		}

		gpuOpt := sdgpu.NewOptimizerAdapter()
		freshCfg.ApplyBackendToProcess(gpuOpt)
		freshCfg.ApplyInstallDefaults()
		freshCfg.ResolvePaths()

		if err := saveConfig(cfgFile, &freshCfg); err != nil {
			log.Fatalf("Failed to save config: %v", err)
		}

		*cfg = freshCfg
	}

	d := initApp(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	deps := tui.ServerDeps{
		Port:    cfg.Port,
		DataDir: cfg.DataDir,
		EnsureAllInstalled: func() {
			d.ensureInstalled()
		},
		InstallStatus: func() map[string]tui.ComponentInstallStatus {
			statuses := d.inst.Status()
			result := make(map[string]tui.ComponentInstallStatus, len(statuses))
			for k, s := range statuses {
				result[k] = tui.ComponentInstallStatus{
					Key:        s.Key,
					Installed:  s.Installed,
					Installing: s.Installing,
					Progress:   s.Progress,
					Error:      s.Error,
					Version:    s.Version,
				}
			}
			return result
		},
		StartAll: func() {
			d.pm.StartAll()
		},
		StartMonitors: func() {
			d.startMonitors(ctx)
		},
		ProcStatus: func() map[string]tui.ServiceInfo {
			statuses := d.pm.Status()
			healthResults := d.hm.Results()
			result := make(map[string]tui.ServiceInfo, len(statuses))
			for k, ps := range statuses {
				si := tui.ServiceInfo{
					Name:     ps.Name,
					Status:   ps.Status,
					PID:      ps.PID,
					Uptime:   ps.Uptime,
					Category: ps.Category,
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
			return d.pm.Start(name)
		},
		StopProc: func(name string) error {
			return d.pm.Stop(name)
		},
		RestartProc: func(name string) error {
			return d.pm.Restart(name)
		},
		ProcLogs: func(name string, lines int) []string {
			logs, err := d.pm.Logs(name, lines)
			if err != nil {
				return nil
			}
			return logs
		},
		GPUInfo: func() tui.GPUInfo {
			info := d.gm.Info()
			return tui.GPUInfo{
				Name:        info.Name,
				MemoryTotal: info.MemoryTotal,
				MemoryUsed:  info.MemoryUsed,
				Utilization: info.Utilization,
				Available:   info.Available,
			}
		},
		HealthResults: func() map[string]tui.HealthResult {
			results := d.hm.Results()
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

	d.inst.OnProgress = func(key, progress string) {
		p.Send(tui.InstallProgressMsg(key, progress))
	}

	d.pm.OnChange = func() {
		p.Send(tui.ServicesChangeMsg{})
	}

	d.gm.OnUpdate = func(info sdgpu.GPUInfo) {
		p.Send(tui.GPUUpdateMsgFunc(tui.GPUInfo{
			Name:        info.Name,
			MemoryTotal: info.MemoryTotal,
			MemoryUsed:  info.MemoryUsed,
			Utilization: info.Utilization,
			Available:   info.Available,
		}))
	}

	srv := d.newServer()

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		srv.Shutdown(shutdownCtx)
		d.shutdown()
	}()

	mdns := d.registerMDNS()

	if _, err := p.Run(); err != nil {
		log.Fatalf("TUI error: %v", err)
	}

	go func() {
		time.Sleep(3 * time.Second)
		os.Exit(0)
	}()

	cancel()
	d.shutdown()
	mdns.Shutdown()
	os.Exit(0)
}

func saveConfig(path string, cfg *config.Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	header := []byte("# SD Studio Server Configuration\n\n")
	return os.WriteFile(path, append(header, data...), 0644)
}

func setupMux(handlers *Handlers, inst *installer.Installer, mm *models.ModelManager, bm *BackendManager, proxy *ProxyHandler) *http.ServeMux {
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
			api.WriteJSON(w, map[string]interface{}{
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

func ensureOllamaBinary(cfg *config.Config, pm *process.ProcessManager, inst *installer.Installer) {
	if ollamaPath := inst.EnsureOllama(); ollamaPath != "" {
		if pc, ok := cfg.Processes["ollama"]; ok {
			pc.Binary = ollamaPath
			cfg.Processes["ollama"] = pc
		}
		pm.UpdateProcessConfig("ollama", ollamaPath, nil, "")
	}
}
