package dns01broker

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

const (
	testTSIGKeyName = "acme-dnsupdate"
	testTSIGSecret  = "dGVzdHNlY3JldA==" // base64("testsecret")
	testZone        = "services.example.com"
)

type capturedUpdate struct {
	mu         sync.Mutex
	tsigOK     bool
	updateZone string
	inserts    []string
	removes    []string
}

func (c *capturedUpdate) snapshot() capturedUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return capturedUpdate{tsigOK: c.tsigOK, updateZone: c.updateZone, inserts: append([]string(nil), c.inserts...), removes: append([]string(nil), c.removes...)}
}

// startTSIGServer runs an in-process DNS server that requires the same TSIG key
// and records the DNS UPDATE it receives, so the test verifies the writer builds
// a correctly-signed, correctly-shaped update without a real PowerDNS.
func startTSIGServer(t *testing.T, captured *capturedUpdate) string {
	t.Helper()
	fqKey := dns.Fqdn(testTSIGKeyName)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &dns.Server{
		Listener:   listener,
		TsigSecret: map[string]string{fqKey: testTSIGSecret},
		// The default accept func rejects the UPDATE opcode with NOTIMP before the
		// handler runs; accept it so the test exercises the real update path.
		MsgAcceptFunc: func(dns.Header) dns.MsgAcceptAction { return dns.MsgAccept },
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			captured.mu.Lock()
			captured.tsigOK = w.TsigStatus() == nil
			if len(req.Question) > 0 {
				captured.updateZone = req.Question[0].Name
			}
			for _, rr := range req.Ns {
				txt, ok := rr.(*dns.TXT)
				if !ok {
					continue
				}
				entry := rr.Header().Name + "=" + strings.Join(txt.Txt, "")
				if rr.Header().Class == dns.ClassNONE {
					captured.removes = append(captured.removes, entry)
				} else {
					captured.inserts = append(captured.inserts, entry)
				}
			}
			captured.mu.Unlock()
			reply := new(dns.Msg)
			reply.SetReply(req)
			if req.IsTsig() != nil {
				reply.SetTsig(fqKey, dns.HmacSHA256, 300, time.Now().Unix())
			}
			_ = w.WriteMsg(reply)
		}),
	}
	go func() { _ = server.ActivateAndServe() }()
	t.Cleanup(func() { _ = server.Shutdown() })
	return listener.Addr().String()
}

func TestPowerDNSWriterPresentSignsAndShapesUpdate(t *testing.T) {
	captured := &capturedUpdate{}
	addr := startTSIGServer(t, captured)

	writer, err := NewPowerDNSWriter(addr, testZone, testTSIGKeyName, "hmac-sha256", testTSIGSecret)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}

	const fqdn = "_acme-challenge.acme-prod.services.example.com"
	if err := writer.Present(fqdn, "challenge-token-abc"); err != nil {
		t.Fatalf("present: %v", err)
	}
	got := captured.snapshot()
	if !got.tsigOK {
		t.Fatal("server did not accept the TSIG signature — the update was not properly signed")
	}
	if got.updateZone != dns.Fqdn(testZone) {
		t.Fatalf("update zone = %q, want %q", got.updateZone, dns.Fqdn(testZone))
	}
	want := dns.Fqdn(fqdn) + "=challenge-token-abc"
	if len(got.inserts) != 1 || got.inserts[0] != want {
		t.Fatalf("inserts = %v, want [%q]", got.inserts, want)
	}

	if err := writer.CleanUp(fqdn, "challenge-token-abc"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	got = captured.snapshot()
	if len(got.removes) != 1 || got.removes[0] != want {
		t.Fatalf("removes = %v, want [%q]", got.removes, want)
	}
}

func TestNewPowerDNSWriterRejectsBadInputs(t *testing.T) {
	if _, err := NewPowerDNSWriter("ns:53", testZone, testTSIGKeyName, "hmac-md5", testTSIGSecret); err == nil {
		t.Fatal("expected an error for an unsupported TSIG algorithm")
	}
	if _, err := NewPowerDNSWriter("", testZone, testTSIGKeyName, "hmac-sha256", testTSIGSecret); err == nil {
		t.Fatal("expected an error for a missing nameserver")
	}
}
