package dns01broker

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func rrValue(rr dns.RR) string {
	switch v := rr.(type) {
	case *dns.TXT:
		return strings.Join(v.Txt, "")
	case *dns.A:
		return v.A.String()
	default:
		return ""
	}
}

const (
	testTSIGKeyName = "acme-dnsupdate"
	testTSIGSecret  = "dGVzdHNlY3JldA==" // base64("testsecret")
	testZone        = "services.example.com"
)

type capturedUpdate struct {
	mu          sync.Mutex
	tsigOK      bool
	updateZone  string
	inserts     []string
	removes     []string
	rrsetClears []string
}

func (c *capturedUpdate) snapshot() capturedUpdate {
	c.mu.Lock()
	defer c.mu.Unlock()
	return capturedUpdate{
		tsigOK:      c.tsigOK,
		updateZone:  c.updateZone,
		inserts:     append([]string(nil), c.inserts...),
		removes:     append([]string(nil), c.removes...),
		rrsetClears: append([]string(nil), c.rrsetClears...),
	}
}

// startTSIGServer runs an in-process DNS server requiring the test TSIG key.
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
		// The default accept func rejects UPDATE with NOTIMP before the handler runs.
		MsgAcceptFunc: func(dns.Header) dns.MsgAcceptAction { return dns.MsgAccept },
		Handler: dns.HandlerFunc(func(w dns.ResponseWriter, req *dns.Msg) {
			captured.mu.Lock()
			captured.tsigOK = w.TsigStatus() == nil
			if len(req.Question) > 0 {
				captured.updateZone = req.Question[0].Name
			}
			for _, rr := range req.Ns {
				switch rr.Header().Class {
				case dns.ClassANY:
					captured.rrsetClears = append(captured.rrsetClears, fmt.Sprintf("%s %s", rr.Header().Name, dns.TypeToString[rr.Header().Rrtype]))
				case dns.ClassNONE:
					captured.removes = append(captured.removes, rr.Header().Name+"="+rrValue(rr))
				default:
					captured.inserts = append(captured.inserts, rr.Header().Name+"="+rrValue(rr))
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

func wantExactly(t *testing.T, label string, got []string, want string) {
	t.Helper()
	if len(got) != 1 || got[0] != want {
		t.Fatalf("%s = %v, want [%q]", label, got, want)
	}
}

func TestPowerDNSWriterUpsertAClearsThenInserts(t *testing.T) {
	captured := &capturedUpdate{}
	addr := startTSIGServer(t, captured)

	writer, err := NewPowerDNSWriter(addr, testZone, testTSIGKeyName, "hmac-sha256", testTSIGSecret)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}

	const fqdn = "*.team-dev.services.example.com"
	if err := writer.UpsertA(fqdn, "127.0.0.1"); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	got := captured.snapshot()
	if !got.tsigOK {
		t.Fatal("server did not accept the TSIG signature — the update was not properly signed")
	}
	wantExactly(t, "rrsetClears", got.rrsetClears, dns.Fqdn(fqdn)+" A")
	wantExactly(t, "inserts", got.inserts, dns.Fqdn(fqdn)+"=127.0.0.1")

	if err := writer.UpsertA(fqdn, "203.0.113.10"); err != nil {
		t.Fatalf("upsert a (repoint): %v", err)
	}
	got = captured.snapshot()
	if len(got.rrsetClears) != 2 {
		t.Fatalf("rrsetClears = %v, want 2 clears across both upserts", got.rrsetClears)
	}
	if len(got.inserts) != 2 || got.inserts[1] != dns.Fqdn(fqdn)+"=203.0.113.10" {
		t.Fatalf("inserts = %v, want the second upsert to insert the new value", got.inserts)
	}
}

func TestPowerDNSWriterDeleteAClearsRRset(t *testing.T) {
	captured := &capturedUpdate{}
	addr := startTSIGServer(t, captured)

	writer, err := NewPowerDNSWriter(addr, testZone, testTSIGKeyName, "hmac-sha256", testTSIGSecret)
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}

	const fqdn = "*.team-dev.services.example.com"
	if err := writer.DeleteA(fqdn); err != nil {
		t.Fatalf("delete a: %v", err)
	}
	got := captured.snapshot()
	wantClear := dns.Fqdn(fqdn) + " A"
	if len(got.rrsetClears) != 1 || got.rrsetClears[0] != wantClear {
		t.Fatalf("rrsetClears = %v, want [%q]", got.rrsetClears, wantClear)
	}
	if len(got.inserts) != 0 {
		t.Fatalf("inserts = %v, want none — delete must not insert anything", got.inserts)
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
