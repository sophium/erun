package edgeauth

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// gorillaSubprotocolBearerToken is the parse attachSubprotocolBearerToken replaced:
// gorilla's own Subprotocols, which the attach upgrader still negotiates with, so
// the two must agree on every offer.
func gorillaSubprotocolBearerToken(req *http.Request) string {
	protocols := websocket.Subprotocols(req)
	if len(protocols) != 2 || protocols[0] != AttachAuthSubprotocol {
		return ""
	}
	return strings.TrimSpace(protocols[1])
}

func TestAttachSubprotocolTokenAcceptsExactlyTheOffersGorillaSplits(t *testing.T) {
	scheme := AttachAuthSubprotocol
	offers := [][]string{
		nil,
		{""},
		{" "},
		{","},
		{" , "},
		{scheme},
		{scheme + ","},
		{scheme + ", "},
		{scheme + ",tok"},
		{scheme + ", tok"},
		{"  " + scheme + " ,  tok  "},
		{"\t" + scheme + "\t,\ttok\r\n"},
		{"\u00a0" + scheme + "\u3000,\u2003tok\u0085"},
		{scheme + "\u200b, tok"},
		{scheme + ", tok with spaces"},
		{scheme + ", tok,"},
		{scheme + ", tok, other"},
		{scheme + ",,tok"},
		{"," + scheme + ", tok"},
		{"other, tok"},
		{strings.ToUpper(scheme) + ", tok"},
		{scheme, "tok"},
		{scheme + ", tok", "other"},
		{"", scheme + ", tok"},
	}
	for _, values := range offers {
		req := httptest.NewRequest(http.MethodGet, "/mcp/attach/session-1", nil)
		for _, value := range values {
			req.Header.Add("Sec-WebSocket-Protocol", value)
		}
		if got, want := attachSubprotocolBearerToken(req), gorillaSubprotocolBearerToken(req); got != want {
			t.Errorf("offers %q: token = %q, want %q", values, got, want)
		}
	}
}

// With no Authorization header, attach reads its token from Sec-WebSocket-Protocol
// before anything is verified; splitting that header on every comma would allocate
// an entry per separator before the two-entry check rejects it.
func TestAttachSubprotocolParsingDoesNotScaleWithSeparators(t *testing.T) {
	issuer, _ := identityWithToken(t)
	cfg := McpAuthConfig{trustedIssuers: map[string]string{issuer: "acme"}, audience: "erun-mcp"}
	handler := WsAttachAuthHTTPMiddleware(cfg, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("an offer of 1 MiB of separators reached the attach handler")
	}))
	header := strings.Repeat(",", 1<<20)
	req := httptest.NewRequest(http.MethodGet, "/mcp/attach/session-1", nil)
	req.Header.Set("Sec-WebSocket-Protocol", header)
	rec := httptest.NewRecorder()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	handler.ServeHTTP(rec, req)
	runtime.ReadMemStats(&after)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 1<<18 {
		t.Errorf("allocated %d bytes for a header of %d separators; the work must not grow with them", allocated, len(header))
	}
}
