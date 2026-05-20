package gpuproxy

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
)

type handler struct {
	proxy        *Proxy
	reverseProxy *httputil.ReverseProxy
	target       *url.URL
	name         string
}

func newHandler(p *Proxy, name string, targetURL string) (*handler, error) {
	target, err := url.Parse(targetURL)
	if err != nil {
		return nil, err
	}

	rp := httputil.NewSingleHostReverseProxy(target)
	rp.Transport = &http.Transport{
		ResponseHeaderTimeout: 0,
	}
	rp.FlushInterval = -1

	return &handler{
		proxy:        p,
		reverseProxy: rp,
		target:       target,
		name:         name,
	}, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	priority := PriorityHigh
	if r.Header.Get(h.proxy.config.StudioHeader) != "" {
		priority = PriorityLow
	}
	r.Header.Del(h.proxy.config.StudioHeader)

	qr := h.proxy.queue.Enqueue(h.name, priority)

	select {
	case <-qr.Proceed:
		h.reverseProxy.ServeHTTP(w, r)
		close(qr.Complete)
	case <-r.Context().Done():
		log.Printf("[gpuproxy:%s] request %d cancelled while waiting in queue", h.name, qr.ID)
		h.proxy.queue.Release()
	}
}
