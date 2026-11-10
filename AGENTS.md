# AGENTS.md

## Project Purpose

**Chaosbackend** is a Go HTTP server designed to deliberately misbehave for testing purposes. It simulates various failure modes and degraded performance scenarios—slow responses, connection resets, HTTP errors, and intermittent failures—making it ideal for testing load balancers, reverse proxies (like Varnish), CDNs, and client resilience.

The name reflects its purpose: a "chaos monkey" style backend that helps you verify your infrastructure handles real-world failures gracefully.

## Project Structure

```
.
├── chaosbackend.go    # Main application (single file)
├── chaosbackend_test.go # Handler and parsing tests
├── template.html      # HTML template for the default endpoint (embedded into the binary)
├── go.mod / go.sum    # Go module files
├── Dockerfile         # Multi-stage build for containerized deployment
├── README.md          # User-facing documentation
└── media/             # Logo and assets
```

## Architecture

- **Single-file design**: All logic lives in `chaosbackend.go` for simplicity.
- **Multi-server mode**: Can bind to multiple addresses and ports simultaneously using goroutines.
- **Per-listener state**: Each address/port gets its own mux (`newMux`), so `X-Backend` and `/health` state are per listener.
- **Middleware**: `middleware()` sets `X-Backend`, applies `header=` params and logs every request. It wraps the ResponseWriter, so use `http.NewResponseController` for Flush/Hijack.
- **HTTP/HTTPS**: Optional TLS support via `-cert` and `-key` flags. HTTP/2 is disabled so hijacking works.
- **Graceful shutdown**: SIGINT/SIGTERM cancels in-flight sleeps and shuts servers down.

## Faults are query parameters

Every path goes through `chaosHandler`, which parses the query into a `plan` (`parsePlan`, dice rolled up front) and applies it in order: headers (middleware), `sleep`, `reset`, status and body, `sleepBetweenBytes`, `truncate`. New faults should be new parameters that compose with the others, not new endpoints.

| Parameter | Behavior |
|-----------|----------|
| `status` | Status code, `503,50` = 50% chance |
| `sleep` | `base,span,pct` delay before headers (`slow` is an alias) |
| `sleepBetweenBytes` | Stream the body byte by byte with this delay |
| `reset` | Close the connection with no response, `1,20` = 20% chance |
| `truncate` | Abort after N body bytes (default half) despite Content-Length, `N,pct` |
| `bytes` | Generated body of N bytes |
| `chunked` | Omit Content-Length |
| `echo` | Body is the dumped request |
| `header` | `Name:Value`, repeatable, applied in middleware to every response |

| Endpoint | Behavior |
|----------|----------|
| `/` (no query) | HTML documentation page |
| `/error`, `/reset` | Presets: default values for `status=500` / `reset` (see `presets`) |
| `/slow`, `/new` | Legacy URLs, handled like any path |
| `/health` | Health check, flipped per listener with `/health/up`, `/health/down`, `/health/toggle` |

## Coding Conventions

- **Standard library only**: No external dependencies beyond Go's stdlib.
- **Simplicity over abstraction**: This is a testing tool, not production infrastructure. Keep it hackable.
- **Logging**: Use `log` package for request/response visibility—essential for debugging proxy behavior.
- **Validate input**: Bad query parameters return 400. Status codes outside 200-999 make net/http panic, so always go through `parseStatus`.
- **Sleeps**: Use `sleep(r.Context(), d)` so delays stop when the client disconnects.

## Build & Run

```sh
# Build
go build -o chaosbackend

# Run (default: 127.0.0.1:8080)
./chaosbackend

# Multi-address, multi-port
./chaosbackend -a 127.0.0.1,0.0.0.0 -p 8080,8081,9000-9002

# HTTPS
./chaosbackend -p 8443 -cert server.crt -key server.key

# Docker
docker build -t chaosbackend .
docker run -p 8080:8080 chaosbackend
```

## Testing Philosophy

Tests live in `chaosbackend_test.go` and run against `httptest` servers (`go test -race ./...`). When adding tests:
- Focus on handler behavior (status codes, delays, connection handling)
- Test edge cases: invalid input, zero values, port range parsing
- Integration tests should verify actual network behavior (connection resets, slow streaming)

## Common Tasks

| Task | Command |
|------|---------|
| Format code | `gofmt -w .` |
| Vet code | `go vet ./...` |
| Build | `go build -o chaosbackend` |
| Test | `go test -race ./...` |
| Run with race detector | `go run -race .` |

## Known Limitations & TODOs

- `/health` state is in memory only and resets on restart
- `-template` is parsed once at startup; restart to pick up template edits

## Related Files

- `README.md`: User documentation with examples
- `template.html`: Browsable endpoint documentation served at `/`
- `.goreleaser.yaml`: Release automation configuration
