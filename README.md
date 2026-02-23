# Fetcher

A minimal, production-quality HTTP client CLI tool in Go — inspired by `curl`, built to expose HTTP/TCP/TLS internals.

## Build

**Linux/macOS:**
```bash
go build -o fetcher .
```

**Windows:**
```powershell
go build -o fetcher.exe .
```

## Usage

```
fetcher [options] <url>

Options:
  -H "Key: Value"   Custom header (repeatable)
  -t duration       Request timeout (default: 30s)
  -v                Verbose mode
  -L                Follow redirects
```

## Examples

**Simple GET:**
```bash
fetcher https://example.com
```

**With custom headers:**
```bash
fetcher -H "Authorization: Bearer token123" -H "Accept: application/json" https://api.example.com/data
```

**Verbose HTTPS request:**
```bash
fetcher -v https://example.com
```

Sample output (diagnostics → stderr, body → stdout):
```
──────────────────────────────────────────────────
  TIMINGS
──────────────────────────────────────────────────
  DNS resolution:              1.243ms
  TCP connect:                 12.871ms
  TLS handshake:               54.302ms
  Time to first byte:          82.541ms
  Total duration:              83.120ms
──────────────────────────────────────────────────
  RESPONSE
──────────────────────────────────────────────────
  Status:  200 OK
  Proto:   HTTP/2.0
──────────────────────────────────────────────────
  RESPONSE HEADERS
──────────────────────────────────────────────────
  Content-Type: text/html; charset=UTF-8
──────────────────────────────────────────────────
  TLS
──────────────────────────────────────────────────
  Version:      TLS 1.3
  Cipher suite: TLS_AES_128_GCM_SHA256
  Server name:  example.com
  Cert subject: www.example.org
  Cert expires: Mon, 26 May 2025 23:59:59 UTC
──────────────────────────────────────────────────
```

**Follow redirects with custom timeout:**
```bash
fetcher -L -t 10s https://httpbin.org/redirect/3
```

**Save body to file (diagnostics still visible):**
```bash
fetcher -v https://example.com > output.html
```

## Architecture

| Concern | Implementation |
|---|---|
| Transport | Custom `http.Transport` with per-phase timeouts |
| TLS | `crypto/tls`, TLS 1.2 minimum, full state exposed |
| Connection pooling | `MaxIdleConns: 100`, `MaxIdleConnsPerHost: 10` |
| Timings | `net/http/httptrace.ClientTrace` hooks |
| Body streaming | `io.Copy(os.Stdout, resp.Body)` — zero full-buffering |
| Diagnostics | Written to `stderr`; body to `stdout` (Unix-pipeable) |

## Design Decisions

- **stderr for diagnostics, stdout for body** — verbose output never corrupts piped data
- **No redirect by default** — explicit `-L` mirrors curl; uses `http.ErrUseLastResponse`  
- **`httptrace`** — hooks into Go's internal HTTP machinery at the correct phase boundaries without reimplementing the stack
- **`io.Copy` streaming** — memory usage is O(32KB copy buffer) regardless of response size
- **HTTP/2 enabled** — `ForceAttemptHTTP2: true` on the transport, negotiated via ALPN
- **TLS 1.2 minimum** — `MinVersion: tls.VersionTLS12`; cipher suite chosen by server from Go's secure defaults