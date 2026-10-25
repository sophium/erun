package clouddefaults

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestVerifyCloudflareTokenAtClassifiesResponses(t *testing.T) {
	t.Run("rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"Invalid API Token"}]}`))
		}))
		defer srv.Close()
		if _, err := verifyCloudflareTokenAt(srv.URL, "tok"); err == nil || !strings.Contains(err.Error(), "Invalid API Token") {
			t.Fatalf("expected rejection error, got %v", err)
		}
	})

	t.Run("disabled-token", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":true,"result":{"id":"abc","status":"disabled"}}`))
		}))
		defer srv.Close()
		if _, err := verifyCloudflareTokenAt(srv.URL, "tok"); err == nil || !strings.Contains(err.Error(), "disabled") {
			t.Fatalf("expected disabled error, got %v", err)
		}
	})
}

func TestListCloudflareAccountsAtSurfacesRejection(t *testing.T) {
	rejected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"bad token"}]}`))
	}))
	defer rejected.Close()
	if _, err := listCloudflareAccountsAt(rejected.URL, "tok"); err == nil || !strings.Contains(err.Error(), "bad token") {
		t.Fatalf("expected rejection error, got %v", err)
	}
}

func TestResolveCloudflareAccountsViaZones(t *testing.T) {
	t.Run("rejected", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"message":"bad token"}]}`))
		}))
		defer srv.Close()
		if _, err := resolveCloudflareAccountsViaZones(srv.URL, "tok"); err == nil || !strings.Contains(err.Error(), "bad token") {
			t.Fatalf("expected rejection error, got %v", err)
		}
	})
}
