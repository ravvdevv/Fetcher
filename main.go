package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"strings"
	"time"
)

// headerFlags allows multiple -H flags to be specified.
type headerFlags []string

func (h *headerFlags) String() string { return strings.Join(*h, ", ") }
func (h *headerFlags) Set(v string) error {
	*h = append(*h, v)
	return nil
}

// timing captures connection lifecycle timestamps.
type timing struct {
	start        time.Time
	dnsStart     time.Time
	dnsDone      time.Time
	connectStart time.Time
	connectDone  time.Time
	tlsStart     time.Time
	tlsDone      time.Time
	firstByte    time.Time
}

func main() {
	var (
		headers     headerFlags
		timeout     = flag.Duration("t", 30*time.Second, "request timeout (e.g. 10s, 500ms)")
		verbose     = flag.Bool("v", false, "verbose mode: show timings, headers, TLS info")
		followRedir = flag.Bool("L", false, "follow redirects")
	)
	flag.Var(&headers, "H", "custom header in 'Key: Value' format (repeatable)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: fetcher [options] <url>\n\nOptions:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}
	url := flag.Arg(0)

	if err := run(url, headers, *timeout, *verbose, *followRedir); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(url string, headers headerFlags, timeout time.Duration, verbose, followRedir bool) error {
	t := &timing{start: time.Now()}

	// Build a custom transport with sane defaults and instrumentation hooks.
	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}

	transport := &http.Transport{
		// Connection pooling settings.
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,

		// Per-phase timeouts.
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		ExpectContinueTimeout: 1 * time.Second,

		TLSClientConfig:    tlsCfg,
		ForceAttemptHTTP2:  true,
		DisableCompression: false,
	}

	client := &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}

	if !followRedir {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	// Build request with optional trace.
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("User-Agent", "fetcher/1.0")

	for _, h := range headers {
		parts := strings.SplitN(h, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid header %q (expected 'Key: Value')", h)
		}
		req.Header.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
	}

	var connState *tls.ConnectionState

	if verbose {
		trace := &httptrace.ClientTrace{
			DNSStart: func(_ httptrace.DNSStartInfo) {
				t.dnsStart = time.Now()
			},
			DNSDone: func(_ httptrace.DNSDoneInfo) {
				t.dnsDone = time.Now()
			},
			ConnectStart: func(_, _ string) {
				t.connectStart = time.Now()
			},
			ConnectDone: func(_, _ string, _ error) {
				t.connectDone = time.Now()
			},
			TLSHandshakeStart: func() {
				t.tlsStart = time.Now()
			},
			TLSHandshakeDone: func(cs tls.ConnectionState, _ error) {
				t.tlsDone = time.Now()
				connState = &cs
			},
			GotFirstResponseByte: func() {
				t.firstByte = time.Now()
			},
		}
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	totalTime := time.Since(t.start)

	if verbose {
		printVerbose(t, totalTime, resp, connState)
	}

	// Stream body directly to stdout — no buffering.
	if _, err := io.Copy(os.Stdout, resp.Body); err != nil {
		return fmt.Errorf("reading body: %w", err)
	}

	return nil
}

func printVerbose(t *timing, total time.Duration, resp *http.Response, cs *tls.ConnectionState) {
	sep := strings.Repeat("─", 50)
	f := func(label string, d time.Duration) {
		fmt.Fprintf(os.Stderr, "  %-28s %v\n", label, d.Round(time.Microsecond))
	}

	fmt.Fprintln(os.Stderr, sep)
	fmt.Fprintln(os.Stderr, "  TIMINGS")
	fmt.Fprintln(os.Stderr, sep)

	if !t.dnsDone.IsZero() {
		f("DNS resolution:", t.dnsDone.Sub(t.dnsStart))
	}
	if !t.connectDone.IsZero() {
		f("TCP connect:", t.connectDone.Sub(t.connectStart))
	}
	if cs != nil && !t.tlsDone.IsZero() {
		f("TLS handshake:", t.tlsDone.Sub(t.tlsStart))
	}
	if !t.firstByte.IsZero() {
		f("Time to first byte:", t.firstByte.Sub(t.start))
	}
	f("Total duration:", total)

	fmt.Fprintln(os.Stderr, sep)
	fmt.Fprintln(os.Stderr, "  RESPONSE")
	fmt.Fprintln(os.Stderr, sep)
	fmt.Fprintf(os.Stderr, "  Status:  %s\n", resp.Status)
	fmt.Fprintf(os.Stderr, "  Proto:   %s\n", resp.Proto)

	fmt.Fprintln(os.Stderr, sep)
	fmt.Fprintln(os.Stderr, "  RESPONSE HEADERS")
	fmt.Fprintln(os.Stderr, sep)
	for k, vv := range resp.Header {
		for _, v := range vv {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", k, v)
		}
	}

	if cs != nil {
		fmt.Fprintln(os.Stderr, sep)
		fmt.Fprintln(os.Stderr, "  TLS")
		fmt.Fprintln(os.Stderr, sep)
		fmt.Fprintf(os.Stderr, "  Version:      %s\n", tlsVersionName(cs.Version))
		fmt.Fprintf(os.Stderr, "  Cipher suite: %s\n", tls.CipherSuiteName(cs.CipherSuite))
		fmt.Fprintf(os.Stderr, "  Server name:  %s\n", cs.ServerName)
		if len(cs.PeerCertificates) > 0 {
			cert := cs.PeerCertificates[0]
			fmt.Fprintf(os.Stderr, "  Cert subject: %s\n", cert.Subject.CommonName)
			fmt.Fprintf(os.Stderr, "  Cert expires: %s\n", cert.NotAfter.Format(time.RFC1123))
		}
	}
	fmt.Fprintln(os.Stderr, sep)
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return fmt.Sprintf("unknown (0x%04x)", v)
	}
}
