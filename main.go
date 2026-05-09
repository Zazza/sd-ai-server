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
)

func main() {
	dataDir := flag.String("data", "", "data directory (default: ~/sd-studio-server)")
	configPath := flag.String("config", "", "config file path (default: {data-dir}/server-config.yaml)")
	port := flag.Int("port", 0, "override server port")
	flag.Parse()

	// Resolve data dir first
	dir := *dataDir
	if dir == "" {
		dir = defaultDataDir()
	}
	dir, _ = filepath.Abs(dir)

	// Config path defaults to {data-dir}/server-config.yaml
	cfgFile := *configPath
	if cfgFile == "" {
		cfgFile = filepath.Join(dir, "server-config.yaml")
	}

	// Ensure data directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Fatalf("Failed to create data directory %s: %v", dir, err)
	}

	// Load config
	cfg, err := LoadWithDir(cfgFile, dir)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if *port != 0 {
		cfg.Port = *port
	}

	log.Printf("SD Studio Server starting on port %d (backend: %s)", cfg.Port, cfg.ActiveSD)
	log.Printf("Data directory: %s", cfg.DataDir)

	// Initialize components
	inst := NewInstaller(cfg)
	pm := NewProcessManager(cfg, inst)
	proxy := NewProxyHandler(pm, cfg)
	hm := NewHealthMonitor(cfg)
	gm := NewGPUMonitor()
	mm := NewModelManager(cfg)
	bm := NewBackendManager(cfg)
	handlers := NewHandlers(pm, hm, gm, cfg, inst)

	// Setup HTTP mux
	mux := http.NewServeMux()

	// Register routes
	handlers.RegisterRoutes(mux)
	mm.RegisterRoutes(mux)
	mm.RegisterDeleteRoutes(mux)
	bm.RegisterRoutes(mux)
	inst.RegisterRoutes(mux)

	// Proxy catches /api/sd/*, /api/llm/*, /api/rembg/*
	mux.Handle("/api/sd/", proxy)
	mux.Handle("/api/llm/", proxy)
	mux.Handle("/api/rembg/", proxy)

	// Root endpoint
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

	// Start context for background goroutines
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Install all components in dependency order (python → forge → ollama → rembg)
	inst.EnsureAllInstalled()

	// Start auto-start processes
	pm.StartAll()

	// Start background monitors
	go pm.Watch(ctx)
	go hm.Start(ctx)
	go gm.Start(ctx)

	// Setup HTTP server
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      corsMiddleware(mux),
		ReadTimeout:  0, // SD uploads can be large
		WriteTimeout: 0, // SD generation can take minutes
	}

	// Start mDNS
	mdns := NewMDNS(cfg.Port, cfg.MDNS)
	if err := mdns.Register(); err != nil {
		log.Printf("mDNS registration failed (non-fatal): %v", err)
	} else if cfg.MDNS {
		log.Printf("mDNS: registered _sd-studio._tcp on port %d", cfg.Port)
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		cancel() // stop background goroutines

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
