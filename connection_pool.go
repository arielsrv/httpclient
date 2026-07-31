package httpclient

import (
	"net/http"
	"time"
)

// ConnectionPool wraps net/http Transport with sensible defaults.
// Use NewConnectionPool() for default or NewConnectionPoolWithOptions() to customize.
type ConnectionPool struct {
	transport *http.Transport
}

type ConnectionPoolOptions struct {
	MaxIdleConns        int
	MaxIdleConnsPerHost int
	MaxConnsPerHost     int
	IdleConnTimeout     time.Duration
}

var defaultPoolOptions = ConnectionPoolOptions{
	MaxIdleConns:        100,
	MaxIdleConnsPerHost: 10,
	MaxConnsPerHost:     0, // unlimited
	IdleConnTimeout:     90 * time.Second,
}

func NewConnectionPool() *ConnectionPool {
	return NewConnectionPoolWithOptions(defaultPoolOptions)
}

func NewConnectionPoolWithOptions(opts ConnectionPoolOptions) *ConnectionPool {
	return &ConnectionPool{
		transport: &http.Transport{
			MaxIdleConns:        opts.MaxIdleConns,
			MaxIdleConnsPerHost: opts.MaxIdleConnsPerHost,
			MaxConnsPerHost:     opts.MaxConnsPerHost,
			IdleConnTimeout:     opts.IdleConnTimeout,
		},
	}
}
