package main

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
)

type ProxyHandler struct {
	mu      sync.RWMutex
	routes  map[string]*httputil.ReverseProxy
	manager *ProcessManager
}

func NewProxyHandler(pm *ProcessManager, cfg *Config) *ProxyHandler {
	ph := &ProxyHandler{
		routes:  make(map[string]*httputil.ReverseProxy),
		manager: pm,
	}

	for _, pc := range cfg.Processes {
		if pc.ProxyPath == "" || pc.TargetURL == "" {
			continue
		}
		target, err := url.Parse(pc.TargetURL)
		if err != nil {
			continue
		}
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.Transport = &http.Transport{
			ResponseHeaderTimeout: 0, // SD can take very long
		}
		ph.routes[pc.ProxyPath] = proxy
	}

	return ph
}

func (ph *ProxyHandler) UpdateRoute(proxyPath, targetURL string) error {
	target, err := url.Parse(targetURL)
	if err != nil {
		return fmt.Errorf("parse target URL: %w", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{
		ResponseHeaderTimeout: 0,
	}
	ph.mu.Lock()
	ph.routes[proxyPath] = proxy
	ph.mu.Unlock()
	return nil
}

func (ph *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ph.mu.RLock()
	defer ph.mu.RUnlock()

	for prefix, proxy := range ph.routes {
		if strings.HasPrefix(r.URL.Path, prefix) {
			// Strip the proxy prefix so /api/sd/sdapi/v1/options → /sdapi/v1/options
			http.StripPrefix(prefix, proxy).ServeHTTP(w, r)
			return
		}
	}

	http.NotFound(w, r)
}
