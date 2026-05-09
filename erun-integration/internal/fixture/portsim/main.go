// Command portsim is a stand-in for `kubectl port-forward` in integration
// tests. It listens on the requested local TCP port and accepts (then
// immediately closes) any inbound connections so production code's
// "is the local port reachable?" polling succeeds. It runs until killed,
// matching the long-lived behavior of a real port-forward.
//
// Usage: portsim --port PORT
package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
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
		if len(bannerBytes) > 0 {
			_, _ = conn.Write(bannerBytes)
		}
		_ = conn.Close()
	}
}

// decodeBanner unescapes \r and \n so callers can pass "SSH-2.0-test\r\n"
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
