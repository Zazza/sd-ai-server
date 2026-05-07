package main

import (
	"github.com/grandcat/zeroconf"
)

type MDNSService struct {
	server   *zeroconf.Server
	port     int
	enabled  bool
}

func NewMDNS(port int, enabled bool) *MDNSService {
	return &MDNSService{port: port, enabled: enabled}
}

func (m *MDNSService) Register() error {
	if !m.enabled {
		return nil
	}

	server, err := zeroconf.Register(
		"SD Studio Server",
		"_sd-studio._tcp",
		"local.",
		m.port,
		[]string{"version=1.0"},
		nil,
	)
	if err != nil {
		return err
	}
	m.server = server
	return nil
}

func (m *MDNSService) Shutdown() {
	if m.server != nil {
		m.server.Shutdown()
	}
}
