# Chaosbacked
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
| `-template` | Path to HTML template file for the default page | `template.html` |

Example:
```sh
./chaosbackend -a 127.0.0.1,0.0.0.0 -p 8080,8081,9000-9002 -template custom.html
```

## Available Endpoints

| Endpoint | Description |
|----------|-------------|
| `/` | Default page with links and documentation |
| `/error` | Returns a 500 Internal Server Error |
| `/error?status=404` | Returns a 404 Not Found error |
| `/error?status=404&sleep=5000` | Returns a 404 error after 5 seconds |
| `/reset` | Closes the connection immediately (simulates a reset) |
| `/slow?sleep=1000&sleepBetweenBytes=100` | Returns 200 OK, waits 1s before sending, then 0.1s between bytes |
| `/new?status=503,50` | Returns 503 with a 50% chance, otherwise 200 |
| `/new?slow=1000,500,30` | Sleeps 1s plus up to 0.5s extra with 30% chance |
| `/new?reset=1` | Closes the connection immediately |

### Query Parameters

- `status`: HTTP status code to return (optionally with frequency, e.g., `503,50` for 50% chance)
- `sleep`: Time in milliseconds to wait before responding
- `sleepBetweenBytes`: Time in milliseconds to wait between sending each byte
- `slow`: Comma-separated values for sleep, span, and frequency (e.g., `1000,500,30`)
- `reset`: If present, closes the connection

## Example Requests

```sh
curl http://localhost:8080/error
curl http://localhost:8080/error?status=404
curl http://localhost:8080/error?status=404&sleep=5000
curl http://localhost:8080/slow?sleep=1000&sleepBetweenBytes=100
curl http://localhost:8080/reset
curl http://localhost:8080/new?status=503,50
curl http://localhost:8080/new?slow=1000,500,30
curl http://localhost:8080/new?reset=1
```

---
See [template.html](template.html) for a browsable overview.
