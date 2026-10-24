package dns01broker

import (
	"fmt"
	"strings"
	"time"

	"github.com/miekg/dns"
)

var tsigAlgorithms = map[string]string{
	"hmac-sha256": dns.HmacSHA256,
	"hmac-sha512": dns.HmacSHA512,
	"hmac-sha1":   dns.HmacSHA1,
}

const challengeTTL = 60

// PowerDNSWriter writes records over RFC2136 using the central zone TSIG key.
type PowerDNSWriter struct {
	nameserver  string
	zone        string
	tsigKeyName string
	tsigAlgo    string
	client      *dns.Client
}

// NewPowerDNSWriter builds a writer for the services zone.
func NewPowerDNSWriter(nameserver, zone, tsigKeyName, tsigAlgorithm, tsigSecret string) (*PowerDNSWriter, error) {
	algo, ok := tsigAlgorithms[strings.ToLower(strings.TrimSpace(tsigAlgorithm))]
	if !ok {
		return nil, fmt.Errorf("unsupported TSIG algorithm %q", tsigAlgorithm)
	}
	nameserver = strings.TrimSpace(nameserver)
	if nameserver == "" {
		return nil, fmt.Errorf("powerdns nameserver is required")
	}
	keyName := dns.Fqdn(strings.TrimSpace(tsigKeyName))
	if keyName == "." {
		return nil, fmt.Errorf("tsig key name is required")
	}
	// TCP keeps the TSIG-signed message from being truncated.
	client := &dns.Client{
		Net:        "tcp",
		TsigSecret: map[string]string{keyName: strings.TrimSpace(tsigSecret)},
		Timeout:    10 * time.Second,
	}
	return &PowerDNSWriter{
		nameserver:  nameserver,
		zone:        dns.Fqdn(strings.TrimSpace(zone)),
		tsigKeyName: keyName,
		tsigAlgo:    algo,
		client:      client,
	}, nil
}

// Present adds the challenge TXT value at fqdn; re-presenting is idempotent.
func (w *PowerDNSWriter) Present(fqdn, value string) error {
	return w.update(fqdn, value, true)
}

// CleanUp removes one challenge TXT value, leaving concurrent values intact.
func (w *PowerDNSWriter) CleanUp(fqdn, value string) error {
	return w.update(fqdn, value, false)
}

// wildcardTTL matches erun-common's defaultExposeWildcardTTL.
const wildcardTTL = 60

// UpsertA replaces the A RRset at fqdn with a single record.
func (w *PowerDNSWriter) UpsertA(fqdn, value string) error {
	// RemoveRRset and Insert mutate the RR header in place, so each needs its own RR.
	removeRR, err := dns.NewRR(fmt.Sprintf("%s %d IN A %s", dns.Fqdn(fqdn), wildcardTTL, value))
	if err != nil {
		return fmt.Errorf("build A record: %w", err)
	}
	insertRR, err := dns.NewRR(fmt.Sprintf("%s %d IN A %s", dns.Fqdn(fqdn), wildcardTTL, value))
	if err != nil {
		return fmt.Errorf("build A record: %w", err)
	}
	msg := new(dns.Msg)
	msg.SetUpdate(w.zone)
	msg.RemoveRRset([]dns.RR{removeRR})
	msg.Insert([]dns.RR{insertRR})
	return w.exchange(msg)
}

// DeleteA removes the A RRset at fqdn. RemoveRRset zeroes rdlength, so the
// placeholder address never reaches the wire.
func (w *PowerDNSWriter) DeleteA(fqdn string) error {
	rr, err := dns.NewRR(fmt.Sprintf("%s 0 IN A 0.0.0.0", dns.Fqdn(fqdn)))
	if err != nil {
		return fmt.Errorf("build A rrset marker: %w", err)
	}
	msg := new(dns.Msg)
	msg.SetUpdate(w.zone)
	msg.RemoveRRset([]dns.RR{rr})
	return w.exchange(msg)
}

func (w *PowerDNSWriter) update(fqdn, value string, insert bool) error {
	rr, err := dns.NewRR(fmt.Sprintf("%s %d IN TXT %q", dns.Fqdn(fqdn), challengeTTL, value))
	if err != nil {
		return fmt.Errorf("build TXT record: %w", err)
	}
	msg := new(dns.Msg)
	msg.SetUpdate(w.zone)
	if insert {
		msg.Insert([]dns.RR{rr})
	} else {
		msg.Remove([]dns.RR{rr})
	}
	return w.exchange(msg)
}

func (w *PowerDNSWriter) exchange(msg *dns.Msg) error {
	msg.SetTsig(w.tsigKeyName, w.tsigAlgo, 300, time.Now().Unix())
	reply, _, err := w.client.Exchange(msg, w.nameserver)
	if err != nil {
		return fmt.Errorf("dns update exchange: %w", err)
	}
	if reply.Rcode != dns.RcodeSuccess {
		return fmt.Errorf("dns update rejected: %s", dns.RcodeToString[reply.Rcode])
	}
	return nil
}
