package webservice

import (
	"net/http"

	"webservice/internal/health"
)

const defaultAddr = ":8080"

type config struct {
	addr    string
	handler http.Handler
}

// Option customizes a Service during construction.
type Option func(*config)

// WithAddress overrides the default listen address.
func WithAddress(addr string) Option {
	return func(cfg *config) {
		if addr != "" {
			cfg.addr = addr
		}
	}
}

// WithHandler overrides the default HTTP handler.
func WithHandler(handler http.Handler) Option {
	return func(cfg *config) {
		if handler != nil {
			cfg.handler = handler
		}
	}
}

func defaultConfig() config {
	return config{
		addr:    defaultAddr,
		handler: health.Handler(),
	}
}
