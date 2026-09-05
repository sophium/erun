package dns01broker

import (
	"fmt"
	"strings"
	"time"

	"github.com/miekg/dns"
)

// tsigAlgorithms maps the PowerDNS/cert-manager TSIG algorithm names to the
// miekg/dns fully-qualified constants.
var tsigAlgorithms = map[string]string{
	"hmac-sha256": dns.HmacSHA256,
	"hmac-sha512": dns.HmacSHA512,
	"hmac-sha1":   dns.HmacSHA1,
}

// challengeTTL is short — an ACME challenge TXT lives only for the minutes
// between present and cleanup.
const challengeTTL = 60

// PowerDNSWriter writes ACME challenge TXT records into the self-hosted PowerDNS
// over RFC2136 DNS UPDATE, signed with the central zone TSIG key the broker holds
// on every env's behalf. Centralizing the key here — rather than handing it to
// each env — is what lets issuance be brokered and authorized per tenant.
type PowerDNSWriter struct {
	nameserver  string
	zone        string
	tsigKeyName string
	tsigAlgo    string
	client      *dns.Client
}

// NewPowerDNSWriter builds a writer for the services zone against the PowerDNS
// :53 endpoint. The TSIG secret is the base64 key material minted by the
// erun-powerdns chart and held centrally by the broker.
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
	// DNS UPDATE with a TSIG-signed TXT rides over TCP so the signed message is
	// not truncated.
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

// Present adds the ACME challenge TXT value at fqdn. Insert merges into the
// RRset, so re-presenting the same value (a retried challenge) is idempotent.
func (w *PowerDNSWriter) Present(fqdn, value string) error {
	return w.update(fqdn, value, true)
}

// CleanUp removes the specific ACME challenge TXT value at fqdn, leaving any
// concurrent challenge values on the same name intact.
func (w *PowerDNSWriter) CleanUp(fqdn, value string) error {
	return w.update(fqdn, value, false)
}

// wildcardTTL is the TTL for a tenant-set environment wildcard A record,
// matching erun-common's own defaultExposeWildcardTTL so a record written
// through this route is indistinguishable from one `erun expose` wrote
// directly via pdnsutil.
const wildcardTTL = 60

// UpsertA replaces (not merges) the A RRset at fqdn with a single record
// pointing at value, mirroring `pdnsutil replace-rrset`'s semantics: any
// existing A records at that name are cleared first, so re-pointing an
// environment at a new IP never leaves the old one alongside it.
func (w *PowerDNSWriter) UpsertA(fqdn, value string) error {
	// RemoveRRset and Insert each mutate their RR's header in place (class,
	// ttl, rdlength), so the remove and the insert need their own RR
	// instances -- sharing one would let whichever mutation runs last win for
	// both entries at pack time.
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

// DeleteA removes the entire A RRset at fqdn, symmetric with UpsertA. The
// address is a placeholder: RemoveRRset zeroes the RR's rdlength before pack,
// so its value never reaches the wire.
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
