// Command portsim stands in for `kubectl port-forward`: it stays alive until
// killed and answers erun's reachability probes.
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
	// silent: a forward that outlived its pod; bound, accepting, never answering.
	silent := flag.Bool("silent", false, "Accept connections and never answer them")
	// noListen: a port-forward still retrying, alive but never bound.
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
			// Don't close: a reset is a definite answer to some probes.
			continue
		}
		go serve(conn, bannerBytes)
	}
}

// serve answers one probe: the banner for SSH, otherwise HTTP 200. It drains
// the request first to avoid a reset truncating the client's read.
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
