package platformclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPlatformClientSubmitInviteRequestSendsBodyAndReturnsAccepted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/invite-requests" {
			t.Fatalf("method=%s path=%s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token-1" {
			t.Fatalf("Authorization = %q, want a bearer even though the caller has no tenant yet", got)
		}
		var body PlatformSubmitInviteRequestParams
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.Kind != PlatformInviteRequestKindJoinTenant || body.TenantName != "acme" {
			t.Fatalf("body = %+v", body)
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(PlatformInviteRequest{
			InviteRequestID: "req-1", Kind: PlatformInviteRequestKindJoinTenant,
			TenantName: "acme", Status: PlatformInviteRequestStatusPending,
		})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	request, err := client.SubmitInviteRequest(context.Background(), PlatformSubmitInviteRequestParams{
		Kind: PlatformInviteRequestKindJoinTenant, TenantName: "acme",
	})
	if err != nil {
		t.Fatalf("SubmitInviteRequest: %v", err)
	}
	if request.InviteRequestID != "req-1" || request.Status != PlatformInviteRequestStatusPending {
		t.Fatalf("request = %+v", request)
	}
}

func TestPlatformClientMyInviteRequestNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/invite-requests/mine" {
			t.Fatalf("path = %s", r.URL.Path)
		}
		http.Error(w, "no invite request found", http.StatusNotFound)
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	_, err := client.MyInviteRequest(context.Background())
	if !errors.Is(err, ErrPlatformNotFound) {
		t.Fatalf("err = %v, want ErrPlatformNotFound", err)
	}
}

func TestPlatformClientListInviteRequestsEncodesFilters(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("status") != "PENDING" || r.URL.Query().Get("kind") != PlatformInviteRequestKindCreateTenant {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode([]PlatformInviteRequest{{InviteRequestID: "req-1"}})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	requests, err := client.ListInviteRequests(context.Background(), PlatformListInviteRequestsParams{
		Status: PlatformInviteRequestStatusPending, Kind: PlatformInviteRequestKindCreateTenant,
	})
	if err != nil {
		t.Fatalf("ListInviteRequests: %v", err)
	}
	if len(requests) != 1 || requests[0].InviteRequestID != "req-1" {
		t.Fatalf("requests = %+v", requests)
	}
}

func TestPlatformClientApproveInviteRequestConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/invite-requests/req-1/approve" || r.Method != http.MethodPost {
			t.Fatalf("method=%s path=%s", r.Method, r.URL.Path)
		}
		http.Error(w, "invite request has already been decided", http.StatusConflict)
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	_, err := client.ApproveInviteRequest(context.Background(), "req-1")
	if !errors.Is(err, ErrPlatformConflict) {
		t.Fatalf("err = %v, want ErrPlatformConflict", err)
	}
}

func TestPlatformClientDeclineInviteRequestSendsReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/invite-requests/req-1/decline" || r.Method != http.MethodPost {
			t.Fatalf("method=%s path=%s", r.Method, r.URL.Path)
		}
		var body PlatformDeclineInviteRequestParams
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Reason != "tenant name already in use" {
			t.Fatalf("body = %+v", body)
		}
		_ = json.NewEncoder(w).Encode(PlatformInviteRequest{
			InviteRequestID: "req-1", Status: PlatformInviteRequestStatusDeclined, DeclineReason: body.Reason,
		})
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	request, err := client.DeclineInviteRequest(context.Background(), "req-1", PlatformDeclineInviteRequestParams{
		Reason: "tenant name already in use",
	})
	if err != nil {
		t.Fatalf("DeclineInviteRequest: %v", err)
	}
	if request.Status != PlatformInviteRequestStatusDeclined || request.DeclineReason != "tenant name already in use" {
		t.Fatalf("request = %+v", request)
	}
}

func TestPlatformClientSubmitInviteRequestRateLimitedExposesRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "42")
		http.Error(w, "too many requests, try again later", http.StatusTooManyRequests)
	}))
	defer srv.Close()

	client := NewPlatformClient(srv.URL, staticToken("token-1"))
	_, err := client.SubmitInviteRequest(context.Background(), PlatformSubmitInviteRequestParams{
		Kind: PlatformInviteRequestKindJoinTenant, TenantName: "acme",
	})
	if !errors.Is(err, ErrPlatformRateLimited) {
		t.Fatalf("err = %v, want ErrPlatformRateLimited", err)
	}
	var statusErr *PlatformStatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("err = %v, want *PlatformStatusError", err)
	}
	delay, ok := statusErr.RetryAfter()
	if !ok || delay != 42*time.Second {
		t.Fatalf("RetryAfter() = %v, %v, want 42s, true", delay, ok)
	}
}
