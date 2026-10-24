package integration

// stubOrderingScannerFixtures are keyed by the shape each one pins.
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
		// The literal handed to the constructor is runnable before the binding is assigned.
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
		// NewTLSServer is NewUnstartedServer plus StartTLS, so it serves before returning.
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
		// The local declared later shadows the package-level issuer the handler reads.
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
		// This declaration is in scope at the read, so the later assignment is a finding.
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
