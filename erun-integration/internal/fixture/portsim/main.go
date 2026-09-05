// Command portsim stands in for `kubectl port-forward` in integration tests: it
// stays alive until killed and answers production's protocol-specific
// reachability probes, so an environment looks reachable without a real cluster.
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
	// silent reproduces the forward that outlived its pod: the listener stays
	// bound and accepts every connection, and nothing ever comes back. It is
	// the state a reachability check that stops at the listener calls healthy.
	silent := flag.Bool("silent", false, "Accept connections and never answer them")
	// noListen reproduces a `kubectl port-forward` that is still retrying
	// against a pod that never answers: the process is alive, but it never
	// gets far enough to bind the local port at all. Bound state alone
	// cannot tell this apart from a process that already exited.
	noListen := flag.Bool("no-listen", false, "Stay alive without binding any port")
	flag.Parse()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	if *noListen {
		<-signals
		os.Exit(0)
	}

	if *port <= 0 {
		log.Fatalf("portsim: --port is required")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(*port))
	if err != nil {
		log.Fatalf("portsim: listen on 127.0.0.1:%d: %v", *port, err)
	}
	defer func() { _ = listener.Close() }()

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
		if *silent {
			// Hold the connection open without answering, exactly as the stale
			// forward did. Closing it would surface as a reset, which some
			// probes read as a definite (if unhelpful) answer.
			continue
		}
		go serve(conn, bannerBytes)
	}
}

// serve answers one probe connection. A real port-forward is opaque TCP, but
// production's probes are protocol-specific, so the caller encodes the protocol
// in whether it sets a banner: a banner means SSH (the probe checks for a "SSH-"
// prefix, so we speak first), none means HTTP (the probe needs a real 2xx, so a
// bare accept-and-close won't do). We drain the request before closing to avoid
// a connection-reset race that truncates the client's read.
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
