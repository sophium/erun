package platformclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/model"
)

func TestPlatformClientStatusErrorMapping(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{http.StatusUnauthorized, ErrPlatformUnauthorized},
		{http.StatusForbidden, ErrPlatformForbidden},
		{http.StatusNotFound, ErrPlatformNotFound},
		{http.StatusConflict, ErrPlatformConflict},
		{http.StatusNotImplemented, ErrPlatformNotImplemented},
	}
	for _, tc := range cases {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "detail: "+http.StatusText(tc.status), tc.status)
			}))
			defer srv.Close()

			client := NewPlatformClient(srv.URL, staticToken("token-1"))
			_, err := client.Whoami(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want errors.Is match for %v", err, tc.want)
			}
			if !strings.Contains(err.Error(), "detail: "+http.StatusText(tc.status)) {
				t.Fatalf("err %v does not carry the server's plain-text detail", err)
			}
		})
	}
}

func TestPlatformClientUnrecognizedStatusIsAGenericError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	_, err := client.Whoami(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, sentinel := range []error{ErrPlatformUnauthorized, ErrPlatformForbidden, ErrPlatformNotFound, ErrPlatformConflict, ErrPlatformNotImplemented} {
		if errors.Is(err, sentinel) {
			t.Fatalf("err = %v matched sentinel %v, want no match for a 500", err, sentinel)
		}
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want to carry the server's message", err)
	}
}

func TestPlatformClientEmptyBodyResponseDecodesToZeroValue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	whoami, err := client.Whoami(context.Background())
	if err != nil {
		t.Fatalf("Whoami with empty body: %v", err)
	}
	if whoami.TenantID != "" || whoami.UserID != "" || len(whoami.Roles) != 0 {
		t.Fatalf("whoami = %+v, want zero value", whoami)
	}
}

func TestPlatformClientTrimsTrailingSlashFromBaseURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/platform" {
			t.Fatalf("path = %q, want exactly one leading slash", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(model.PlatformInfo{})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL+"/", nil)
	if _, err := client.Platform(context.Background()); err != nil {
		t.Fatalf("Platform: %v", err)
	}
}

func TestPlatformAuthErrorCodeDistinguishesTheThreeReasonsBehindA401(t *testing.T) {
	for _, code := range []string{"TENANT_UNRESOLVED", "NOT_ENROLLED", "RESOLUTION_FAILED"} {
		t.Run(code, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"code": code, "message": "denied"})
			}))
			defer srv.Close()

			client := NewPlatformClient(srv.URL, staticToken("token-1"))
			_, err := client.Whoami(context.Background())
			if !errors.Is(err, ErrPlatformUnauthorized) {
				t.Fatalf("err = %v, want ErrPlatformUnauthorized", err)
			}
			if got := PlatformAuthErrorCode(err); got != code {
				t.Fatalf("PlatformAuthErrorCode() = %q, want %q", got, code)
			}
		})
	}
}

func TestPlatformAuthErrorCodeUnclassifiedReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	_, err := client.Whoami(context.Background())
	if !errors.Is(err, ErrPlatformUnauthorized) {
		t.Fatalf("err = %v, want ErrPlatformUnauthorized", err)
	}
	if got := PlatformAuthErrorCode(err); got != "" {
		t.Fatalf("PlatformAuthErrorCode() = %q, want empty for an unclassified 401", got)
	}
}

func TestPlatformAuthErrorCodeNonAuthErrorReturnsEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	_, err := client.Whoami(context.Background())
	if !errors.Is(err, ErrPlatformForbidden) {
		t.Fatalf("err = %v, want ErrPlatformForbidden", err)
	}
	if got := PlatformAuthErrorCode(err); got != "" {
		t.Fatalf("PlatformAuthErrorCode() = %q, want empty for a non-401 error", got)
	}
	if got := PlatformAuthErrorCode(nil); got != "" {
		t.Fatalf("PlatformAuthErrorCode(nil) = %q, want empty", got)
	}
}
