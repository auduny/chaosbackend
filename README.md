# Chaosbackend
This is a simple Go server that starts one or more HTTP backends, designed to behave badly based on your input—perfect for testing proxies like Varnish.
<p align="center">
<img src="media/chaosbackend.svg" alt="Chaos backend" width="400" style="max-width:100%;height:auto;" />
</p>

## Usage

```sh
./chaosbackend [flags]
```

### Command-line Flags

| Flag | Description | Default |
|------|-------------|---------|
| `-a` | Comma-separated list of addresses to listen on | `127.0.0.1` |
| `-p` | Comma-separated list of ports or port ranges (e.g., `4000-4020`) | `8080` |
| `-template` | Path to HTML template file for the default page | built-in |
| `-cert` | TLS certificate file (enables HTTPS together with `-key`) | |
| `-key` | TLS private key file | |
| `-version` | Print version and exit | |

Example:
```sh
./chaosbackend -a 127.0.0.1,0.0.0.0 -p 8080,8081,9000-9002 -template custom.html
```

Every address/port combination is an independent backend. Each response carries an `X-Backend` header with the listener that served it, which makes it easy to see how a load balancer distributes traffic.

HTTPS is served as HTTP/1.1 only, so that connection resets work the same as over plain HTTP. A self-signed certificate for testing can be made with:

```sh
openssl req -x509 -newkey rsa:2048 -nodes -keyout server.key -out server.crt -days 365 -subj /CN=localhost
./chaosbackend -p 8443 -cert server.crt -key server.key
```

### Docker

```sh
docker build -t chaosbackend .
docker run -p 8080:8080 chaosbackend
# Custom ports/flags replace the defaults, so include -a 0.0.0.0
docker run -p 9000-9002:9000-9002 chaosbackend -a 0.0.0.0 -p 9000-9002
```

## Fault Parameters

Faults are query parameters, and they work on **any path** and can be combined. Use whatever URLs your cache or proxy needs (`/products/42?status=503,10`), and each URL is still its own cache object.

Parameters marked with *pct* take an optional `,pct` suffix that makes them fire only that percent of the time, rolled per request.

| Parameter | Example | Effect |
|-----------|---------|--------|
| `status` | `status=503` / `status=503,50` | Status code to return, 200-999 (*pct*, otherwise 200) |
| `sleep` | `sleep=1000` / `sleep=1000,500,30` | Wait before sending headers: `base,span,pct` adds 0-`span` ms extra with `pct` chance. `slow` is an older name for it |
| `sleepBetweenBytes` | `sleepBetweenBytes=100` | Headers are sent right away, then the body arrives one byte at a time |
| `reset` | `reset` / `reset=1,20` | Close the TCP connection without a response, after `sleep` (*pct*) |
| `truncate` | `truncate` / `truncate=100,30` | Send `Content-Length` for the full body, close the connection after N bytes (default half) (*pct*) |
| `bytes` | `bytes=1048576` | Body of exactly N bytes instead of the short status message |
| `chunked` | `chunked` | Use chunked encoding instead of `Content-Length` |
| `echo` | `echo` | Body is the request as the backend received it (headers, body, remote address) |
| `header` | `header=Cache-Control:max-age=60` | Add a response header, can be repeated |

They are applied in this order: headers, `sleep`, `reset`, status and body, `sleepBetweenBytes`, `truncate`. Invalid parameters give a `400 Bad Request` explaining what was wrong. Delays are cancelled if the client disconnects.

`/` without parameters shows the documentation page.

### Endpoints

| Endpoint | Description |
|----------|-------------|
| `/error` | Alias: same as any path, but `status` defaults to 500 |
| `/reset` | Alias: same as any path, but `reset` is on by default |
| `/health` | Returns 200 `OK` or 503 `DOWN` |
| `/health/down`, `/health/up`, `/health/toggle` | Changes the health state for this listener only |

## Example Requests

```sh
curl 'http://localhost:8080/error?status=404&sleep=5000'
curl 'http://localhost:8080/page?sleep=1000&sleepBetweenBytes=100'
curl  http://localhost:8080/reset

# A flaky origin: 10% 503s, 1-1.5s latency, 2% resets and 5% truncated bodies
curl 'http://localhost:8080/products/42?status=503,10&sleep=1000,500&reset=1,2&truncate=1,5&bytes=20000'

# Cacheable error to test whether a proxy caches it
curl 'http://localhost:8080/page?status=503&header=Cache-Control:max-age=60'

# Headers right away, then a 10KB body over ~10s (tests between-bytes timeouts)
curl 'http://localhost:8080/big?bytes=10000&sleepBetweenBytes=1'

# See what the proxy forwards
curl 'http://localhost:8080/anything?echo'

# Take this backend out of rotation
curl http://localhost:8080/health/down
```

---
See [template.html](template.html) for a browsable overview.
