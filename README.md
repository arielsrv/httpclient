# httpclient

[![CI](https://github.com/arielsrv/httpclient/actions/workflows/ci.yml/badge.svg)](https://github.com/arielsrv/httpclient/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/arielsrv/httpclient.svg)](https://pkg.go.dev/github.com/arielsrv/httpclient)
[![Go Report Card](https://goreportcard.com/badge/github.com/arielsrv/httpclient)](https://goreportcard.com/report/github.com/arielsrv/httpclient)

A typed HTTP client for Go, built on `net/http`. Responses deserialize into the
type you ask for, non-2xx statuses are values rather than errors, and an optional
RFC 9111 cache (in-memory, Redis or Memcached) sits transparently in front of
`GET`/`HEAD`/`OPTIONS`.

```go
response, err := client.Get[[]User](ctx, "https://api.example.com/users")
```

## Features

- **Typed requests** — `Get[T]`, `Post[T]`, `Put[T]`, `Patch[T]`, `Delete[T]` return `*HTTPResponse[T]` with the body already decoded.
- **Errors that are actually errors** — `err` covers network-level failures only; a 404 is a successful call with `IsSuccess() == false`. Decode error payloads with `As[E]()`.
- **Content negotiation** — JSON, XML and `x-www-form-urlencoded` request bodies; responses decoded from the response `Content-Type` (including `+json` / `+xml` suffixes) or forced by the caller's `Accept` header.
- **Binary downloads** — `Download` / `DownloadAsync` return raw bytes, no codec involved.
- **Async** — every verb has an `…Async` twin returning a `Future[T]` you `Await()`.
- **HTTP cache** — freshness from `Cache-Control` / `Expires`, conditional revalidation via `ETag` / `Last-Modified`, `no-store` / `no-cache` / `Vary: *` honored. Cache writes happen off the request path.
- **Pluggable cache backends** — Ristretto (in-process), Redis, Memcached, or your own `Cache` implementation.
- **Connection pooling** — sensible `http.Transport` defaults, tunable or shareable across clients.

## Requirements

Go **1.27** or newer — the API relies on generic methods (`client.Get[T]`,
`response.As[E]`).

## Install

```sh
go get github.com/arielsrv/httpclient
```

## Quick start

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/arielsrv/httpclient"
)

type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func main() {
	client := httpclient.NewHTTPClient()
	defer client.Close()

	// err is only a network-level failure: connection refused, timeout, ...
	response, err := client.Get[[]User](context.Background(), "https://gorest.co.in/public/v2/users")
	if err != nil {
		log.Fatalf("network error: %v", err)
	}

	// A non-2xx status is not an error — inspect it.
	if !response.IsSuccess() {
		log.Fatalf("unexpected status %d: %s", response.StatusCode(), response.Body())
	}

	for _, user := range response.Data() {
		fmt.Printf("[%d] %s <%s>\n", user.ID, user.Name, user.Email)
	}
}
```

## Responses

| Method | Description |
| --- | --- |
| `Data() T` | The decoded body. Zero value when the status is not successful. |
| `StatusCode() int` | Raw status code. |
| `IsSuccess() bool` | `2xx`. |
| `Body() string` | Raw body bytes as a string. |
| `Headers() http.Header` | Response headers. |
| `As[E]() (E, error)` | Decode the same body into another type — typically an error payload. |
| `Cacheable(ttl) (time.Duration, bool)` | Whether the response may be stored, and for how long. |
| `Revalidate() bool` | Whether a stored copy must be checked with the origin (`no-cache`). |

### Typed errors

```go
type APIError struct {
	Message string `json:"message"`
}

resp, err := client.Get[User](ctx, url)
if err != nil {
	return err // network failure
}
if !resp.IsSuccess() {
	apiErr, decodeErr := resp.As[APIError]()
	if decodeErr != nil {
		return fmt.Errorf("http %d: %s", resp.StatusCode(), resp.Body())
	}
	return fmt.Errorf("http %d: %s", resp.StatusCode(), apiErr.Message)
}
```

## Request bodies and content negotiation

`Post`/`Put`/`Patch` encode the payload with the codec matching the
`Content-Type` header — JSON by default:

```go
client.Post[Created](ctx, url, payload)                              // application/json
client.Post[Created](ctx, url, payload, httpclient.AsXML())          // application/xml
client.Post[Created](ctx, url, payload, httpclient.AsForm())         // x-www-form-urlencoded (`form` tags)
```

Response decoding follows the response `Content-Type`, unless the caller states
an intent with `Accept`:

```go
client.Get[Feed](ctx, url, httpclient.AcceptXML())

icon, err := client.Download(ctx, "https://example.com/favicon.ico") // []byte, no codec
```

Helpers: `AsJSON`, `AsXML`, `AsForm`, `AcceptJSON`, `AcceptXML`, `AcceptBinary`.

## Async

```go
users := client.GetAsync[[]User](ctx, usersURL)
posts := client.GetAsync[[]Post](ctx, postsURL)

usersResp, err := users.Await()
postsResp, err := posts.Await()
```

Cancelling `ctx` unblocks `Await` with an error.

## Caching

Pass a `Cache` and the number of workers that drain the write queue. Cacheable
methods are `GET`, `HEAD` and `OPTIONS`.

```go
cache, err := httpclient.NewInMemoryClientCache(httpclient.InMemoryConfig{
	ByteSize: 8 << 20, // 8 MiB, evicted by cost
})
if err != nil {
	log.Fatal(err)
}
defer cache.Close()

client := httpclient.NewHTTPClient(
	httpclient.WithCache(cache, runtime.NumCPU()-1),
	httpclient.WithDefaultCacheTTL(5*time.Minute),
	httpclient.WithCacheRevalidationWindow(time.Hour),
)
defer client.Close() // drains pending cache writes
```

Behavior:

- Freshness comes from `Cache-Control: max-age`, then `Expires`, then
  `WithDefaultCacheTTL` (5 minutes by default; `0` caches only what the server
  explicitly marks cacheable).
- `no-store`, `max-age=0` and `Vary: *` opt out of storage; `no-cache` forces
  revalidation before reuse.
- A stale entry is kept for the revalidation window and turned into a
  conditional request (`If-None-Match` / `If-Modified-Since`); a `304` refreshes
  it without transferring the body.
- The cache is best-effort: a miss and an unreachable backend are the same thing,
  and the request falls through to the network.
- Keys include the method, URL and the headers that select a representation
  (`Authorization` and `Cookie` among them) — replace the scheme with
  `WithCacheKeyGenerator`.

### Backends

```go
// In-process (Ristretto)
httpclient.NewInMemoryClientCache(httpclient.InMemoryConfig{ByteSize: 64 << 20})

// Redis
redisCache := httpclient.NewRedisClientCache(httpclient.RedisConfig{Host: "localhost", Port: 6379})
if err := redisCache.Ping(ctx); err != nil { log.Fatal(err) }

// Memcached
memcachedCache := httpclient.NewMemcachedClientCache(httpclient.MemcachedConfig{
	Servers: []string{"localhost:11211"},
})
if err := memcachedCache.Ping(); err != nil { log.Fatal(err) }
```

Any type implementing `Cache` works — a missing key must be reported as
`(nil, false, nil)`, never as an error.

## Client options

| Option | Description |
| --- | --- |
| `WithTimeout(d)` | Bounds the whole request (dial + body read). Default `30s`; `0` defers to the context. |
| `WithConnectionPool(pool)` | Use a specific `ConnectionPool` — share one across clients. |
| `WithTransport(rt)` | Any `http.RoundTripper`; handy in tests. |
| `WithFollowRedirects(bool)` | `false` returns the first 3xx as-is instead of following it. |
| `WithCache(cache, workers)` | Enables the HTTP cache and sizes the background write pool. |
| `WithDefaultCacheTTL(d)` | TTL for responses without freshness information. |
| `WithCacheRevalidationWindow(d)` | How long stale entries survive for conditional revalidation. |
| `WithCacheKeyGenerator(g)` | Custom cache key derivation. |

### Connection pool

```go
pool := httpclient.NewConnectionPoolWithOptions(httpclient.ConnectionPoolOptions{
	MaxIdleConns:        100,
	MaxIdleConnsPerHost: 10,
	MaxConnsPerHost:     0, // unlimited
	IdleConnTimeout:     90 * time.Second,
})

client := httpclient.NewHTTPClient(httpclient.WithConnectionPool(pool))
```

## Examples

Runnable programs live in [`examples/`](examples) (its own module, wired through
`go.work`):

| Example | Shows |
| --- | --- |
| `basic` | Typed `GET`, custom headers, status handling |
| `async` | `GetAsync` and `Future.Await` |
| `post_formats` | JSON / XML / form request bodies |
| `accept_formats` | Driving response decoding with `Accept` |
| `typed_errors` | `As[E]` on non-2xx bodies |
| `binary_download` | `Download` for raw bytes |
| `redirects` | `WithFollowRedirects` |
| `cache` | In-memory cache against a local test server |
| `conditional_cache` | `ETag` revalidation and `304` handling |
| `cache_redis`, `cache_memcached` | Distributed backends (see `examples/docker-compose.yml`) |
| `custom_pool`, `shared_pool` | Connection pool tuning and sharing |
| `problem_json` | `application/problem+json` decoding |

```sh
cd examples
go run ./basic

docker compose up -d      # for the redis / memcached examples
go run ./cache_redis
```

## Development

Tasks are defined in [`Taskfile.yml`](Taskfile.yml) ([Task](https://taskfile.dev)):

```sh
task              # download + lint + test
task test         # mocks, full suite, race suite
task test:race    # go test -race -short -count 5
task test:integration  # Redis/Memcached via testcontainers (needs Docker)
task test:nil     # nilaway
task lint         # golangci-lint --fix, gofumpt, betteralign (root + examples)
task download     # go work sync + tidy both modules
task upgrade      # go-mod-upgrade
```

Everything is also reachable without Task, since the linters and generators are
declared as Go tool dependencies:

```sh
go test ./...
go test -race -short -count 5 ./...
go tool golangci-lint run
go tool gofumpt -l .
go tool mockery --config .mockery.yml
go tool govulncheck ./...
```

Notes:

- The suite uses `httptest` servers and needs no network. The cache backend
  tests spin up Redis and Memcached with testcontainers and skip themselves when
  no Docker daemon is available; `-short` skips them outright.
- Mocks (`mock_*.go`) are generated by mockery and committed — regenerate them
  when an interface changes.

## Continuous integration

[`.github/workflows/ci.yml`](.github/workflows/ci.yml) runs on every push and
pull request:

| Job | What it does |
| --- | --- |
| `build` | `go vet` and `go build` for the module and the examples module |
| `lint` | `golangci-lint`, `gofumpt` formatting check, generated-mock drift check |
| `test` | Full suite with coverage, plus `-race -count 5` on the short suite |
| `integration` | Cache backend tests against real Redis and Memcached containers |
| `vuln` | `govulncheck` |

Coverage output is uploaded as a build artifact.