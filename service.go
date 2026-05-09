package webservice

import (
	"context"
	"net/http"

	"webservice/internal/server"
)

// Service exposes the public library API while keeping implementation details internal.
type Service struct {
	server *server.Server
}

// New builds a Service with sensible defaults and optional overrides.
func New(opts ...Option) *Service {
	cfg := defaultConfig()

	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	return &Service{
		server: server.New(cfg.addr, cfg.handler),
	}
}

// Addr returns the configured listen address.
func (s *Service) Addr() string {
	return s.server.Addr()
}

// Handler returns the configured HTTP handler.
func (s *Service) Handler() http.Handler {
	return s.server.Handler()
}

// ListenAndServe starts the HTTP server and shuts it down when the context ends.
func (s *Service) ListenAndServe(ctx context.Context) error {
	return s.server.ListenAndServe(ctx)
}
