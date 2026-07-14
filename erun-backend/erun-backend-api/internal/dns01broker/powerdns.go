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
