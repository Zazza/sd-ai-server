package gpuproxy

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

type GPUInfoer interface {
	Info() GPUInfo
}

type GPUInfo struct {
	Name        string
	MemoryTotal int
	MemoryFree  int
	MemoryUsed  int
	Utilization int
	Available   bool
}

type Stats struct {
	Queue   QueueStats       `json:"queue"`
	Servers []EndpointStatus `json:"servers"`
}

type EndpointStatus struct {
	Name       string `json:"name"`
	ListenAddr string `json:"listen_addr"`
	TargetURL  string `json:"target_url"`
	Running    bool   `json:"running"`
}

type Proxy struct {
	config      Config
	queue       *PriorityQueue
	handlers    map[string]*handler
	servers     []*http.Server
	addrs       map[string]string
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	gpuMonitor  GPUInfoer
}

func New(cfg Config, gpuMonitor GPUInfoer) *Proxy {
	if cfg.GPUSlots < 1 {
		cfg.GPUSlots = 1
	}
	return &Proxy{
		config:     cfg,
		queue:      NewPriorityQueue(cfg.GPUSlots),
		handlers:   make(map[string]*handler),
		servers:    make([]*http.Server, 0),
		addrs:      make(map[string]string),
		gpuMonitor: gpuMonitor,
	}
}

func (p *Proxy) Start() error {
	for name, ep := range p.config.Endpoints {
		h, err := newHandler(p, name, ep.TargetURL)
		if err != nil {
			return fmt.Errorf("gpuproxy endpoint %q: %w", name, err)
		}
		p.handlers[name] = h

		srv := &http.Server{
			Handler: h,
		}
		p.servers = append(p.servers, srv)

		ln, err := net.Listen("tcp", ep.ListenAddr)
		if err != nil {
			return fmt.Errorf("gpuproxy listen %s: %w", name, err)
		}

		p.addrs[name] = ln.Addr().String()

		p.wg.Add(1)
		go func(s *http.Server, n string, listener net.Listener) {
			defer p.wg.Done()
			log.Printf("[gpuproxy] %s listening on %s → %s", n, listener.Addr(), ep.TargetURL)
			if err := s.Serve(listener); err != nil && err != http.ErrServerClosed {
				log.Printf("[gpuproxy] %s error: %v", n, err)
			}
		}(srv, name, ln)
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.dispatch(ctx)
	}()

	return nil
}

func (p *Proxy) Addr(name string) string {
	return p.addrs[name]
}

func (p *Proxy) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, srv := range p.servers {
		srv.Shutdown(ctx)
	}
	p.wg.Wait()
}

func (p *Proxy) Stats() Stats {
	qs := p.queue.Stats()
	servers := make([]EndpointStatus, 0, len(p.config.Endpoints))
	for name, ep := range p.config.Endpoints {
		addr := p.addrs[name]
		if addr == "" {
			addr = ep.ListenAddr
		}
		servers = append(servers, EndpointStatus{
			Name:       name,
			ListenAddr: addr,
			TargetURL:  ep.TargetURL,
			Running:    true,
		})
	}
	return Stats{
		Queue:   qs,
		Servers: servers,
	}
}

func (p *Proxy) dispatch(ctx context.Context) {
	for {
		req, ok := p.queue.Dequeue(ctx)
		if !ok {
			return
		}
		close(req.Proceed)
		<-req.Complete
		p.queue.Release()
		p.waitForVRAM(ctx)
	}
}

func (p *Proxy) waitForVRAM(ctx context.Context) {
	if p.gpuMonitor == nil {
		return
	}
	deadline := time.After(30 * time.Second)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		info := p.gpuMonitor.Info()
		if !info.Available || info.MemoryTotal == 0 {
			return
		}
		if info.MemoryFree*100/info.MemoryTotal >= 50 {
			return
		}
		log.Printf("[gpuproxy] VRAM cooldown: %d/%d MB free, waiting...", info.MemoryFree, info.MemoryTotal)
		select {
		case <-ctx.Done():
			return
		case <-deadline:
			log.Printf("[gpuproxy] VRAM cooldown timeout, proceeding")
			return
		case <-ticker.C:
		}
	}
}
