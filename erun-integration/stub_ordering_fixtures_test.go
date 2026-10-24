package integration

// stubOrderingScannerFixtures are the sources TestStubOrderingScannerFixtures
// holds the stub-ordering scanner to, keyed by the shape each one pins.
var stubOrderingScannerFixtures = map[string]struct {
	src   string
	names []string // the read names the scanner must report, in any order
}{
	"reads_the_server_assigned_by_newserver": {
		src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(server.URL))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}
`,
		names: []string{"server"},
	},
	"reads_a_value_assigned_after_newserver": {
		src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	var issuer string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(issuer))
	})
	server := httptest.NewServer(mux)
	issuer = server.URL
	t.Cleanup(server.Close)
	return server
}
`,
		names: []string{"issuer"},
	},
	"handler_is_the_constructor_argument_reading_the_constructor_binding": {
		// The literal is not registered after the constructor returned; it
		// is what the server starts with, so the binding NewServer performs
		// on its way out is unordered against the handler's read of it.
		src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(server.URL))
	}))
	t.Cleanup(server.Close)
	return server
}
`,
		names: []string{"server"},
	},
	"handler_is_the_constructor_argument_reading_a_later_assignment": {
		src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	var issuer string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(issuer))
	}))
	issuer = server.URL
	t.Cleanup(server.Close)
	return server
}
`,
		names: []string{"issuer"},
	},
	"handler_is_the_constructor_argument_reading_only_prior_values": {
		src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	issuer := "http://example.invalid"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(issuer))
	}))
	t.Cleanup(server.Close)
	return server
}
`,
		names: nil,
	},
	"assigns_before_start": {
		src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	var issuer string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(issuer))
	})
	server := httptest.NewUnstartedServer(mux)
	issuer = "http://" + server.Listener.Addr().String()
	server.Start()
	t.Cleanup(server.Close)
	return server
}
`,
		names: nil,
	},
	"ordinary_closure_called_after_the_server_is_up": {
		src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	report := func() string { return server.URL }
	server := httptest.NewServer(http.NewServeMux())
	t.Cleanup(server.Close)
	_ = report()
	return server
}
`,
		names: nil,
	},
	"handler_registered_after_serving_begins": {
		src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(server.URL))
	})
	t.Cleanup(server.Close)
	return server
}
`,
		names: nil,
	},
	"reads_the_server_assigned_by_newtlsserver": {
		// NewTLSServer serves before it returns exactly as NewServer does:
		// it is NewUnstartedServer followed by StartTLS, so the returned
		// server is already accepting connections while the binding it
		// hands back -- and every value assigned after it -- has no
		// happens-before edge to a handler it was given.
		src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(server.URL))
	})
	server := httptest.NewTLSServer(mux)
	t.Cleanup(server.Close)
	return server
}
`,
		names: []string{"server"},
	},
	"tls_server_handler_reads_only_prior_values": {
		// Widening the serving constructors to NewTLSServer must not turn a
		// correctly ordered TLS stub into a finding.
		src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	issuer := "http://example.invalid"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(issuer))
	}))
	t.Cleanup(server.Close)
	return server
}
`,
		names: nil,
	},
	"started_from_a_go_statement": {
		// `go server.Start()` begins serving too. The call is the same one
		// the ExprStmt case recognises; only the statement wrapper differs,
		// so a scope that starts its stub this way must not be skipped.
		src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	var issuer string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(issuer))
	})
	server := httptest.NewUnstartedServer(mux)
	go server.Start()
	issuer = server.URL
	t.Cleanup(server.Close)
	return server
}
`,
		names: []string{"issuer"},
	},
	"handler_reads_a_package_level_value_shadowed_later": {
		// The handler's `issuer` is the package-level one, which is never
		// written after serving begins. The local declared underneath is a
		// different binding that begins after both the handler's read and
		// the serving site, so matching the two by name alone would red
		// correct code.
		src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

var issuer = "http://example.invalid"

func stub(t testing.TB) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(issuer))
	})
	server := httptest.NewServer(mux)
	issuer := server.URL
	_ = issuer
	t.Cleanup(server.Close)
	return server
}
`,
		names: nil,
	},
	"local_declared_before_the_handler_still_counts": {
		// The shadow exemption must not be a general amnesty for short
		// declarations: this one is in scope at the handler's read, so the
		// later assignment to it is a genuine finding.
		src: `package fixture

import (
	"net/http"
	"net/http/httptest"
)

func stub(t testing.TB) *httptest.Server {
	mux := http.NewServeMux()
	issuer := "http://example.invalid"
	server := httptest.NewUnstartedServer(mux)
	mux.HandleFunc("GET /v1/platform", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(issuer))
	})
	server.Start()
	issuer = server.URL
	t.Cleanup(server.Close)
	return server
}
`,
		names: []string{"issuer"},
	},
}
