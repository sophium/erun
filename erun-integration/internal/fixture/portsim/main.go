// Command portsim is a stand-in for `kubectl port-forward` in integration
// tests. It listens on the requested local TCP port until killed, mirroring
// the long-lived behavior of a real port-forward, and answers production's
// reachability probes on each accepted connection:
//
//   - With --banner set (the sshd port), it writes the banner first so the
//     SSH probe — which reads the server greeting and checks for a "SSH-"
//     prefix — succeeds.
//   - Without a banner (the MCP and API ports), it reads the probe's HTTP
//     request and replies with a minimal "200 OK". The MCP probe (GET /mcp)
//     and the API probe (GET /healthz, which requires a 2xx) both need a real
//     HTTP response, so a bare accept-and-close does not satisfy them.
//
// Usage: portsim --port PORT [--banner "SSH-2.0-test\r\n"]
package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	port := flag.Int("port", 0, "Local TCP port to listen on")
	banner := flag.String("banner", "", "Optional banner to send on each accepted connection (e.g. \"SSH-2.0-test\\r\\n\")")
	flag.Parse()
	if *port <= 0 {
		log.Fatalf("portsim: --port is required")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(*port))
	if err != nil {
		log.Fatalf("portsim: listen on 127.0.0.1:%d: %v", *port, err)
	}
	defer func() { _ = listener.Close() }()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signals
		_ = listener.Close()
		os.Exit(0)
	}()

	bannerBytes := decodeBanner(*banner)
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go serve(conn, bannerBytes)
	}
}

// serve answers a single probe connection. A real kubectl port-forward is
// opaque TCP, but production's reachability checks are protocol-specific, and
// the caller signals which protocol a port speaks by whether it sets a banner:
//
//   - banner set → SSH: the probe reads the server greeting and checks for a
//     "SSH-" prefix, so we write the banner first (server-speaks-first).
//   - no banner → HTTP (MCP /mcp, API /healthz): the probe issues an HTTP
//     request and requires a successful response, so we read the request and
//     reply with a minimal 200. We drain the request before closing so the
//     client reads the full response without a connection-reset race.
func serve(conn net.Conn, bannerBytes []byte) {
	defer func() { _ = conn.Close() }()
	if len(bannerBytes) > 0 {
		_, _ = conn.Write(bannerBytes)
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, _ = conn.Read(make([]byte, 4096))
	_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"))
}

// decodeBanner unescapes \r, \n, and \t so callers can pass "SSH-2.0-test\r\n"
// as a flag value without shell-escape gymnastics.
func decodeBanner(value string) []byte {
	if value == "" {
		return nil
	}
	out := make([]byte, 0, len(value))
	for i := 0; i < len(value); i++ {
		if value[i] == '\\' && i+1 < len(value) {
			switch value[i+1] {
			case 'r':
				out = append(out, '\r')
				i++
				continue
			case 'n':
				out = append(out, '\n')
				i++
				continue
			case 't':
				out = append(out, '\t')
				i++
				continue
			}
		}
		out = append(out, value[i])
	}
	return out
}
