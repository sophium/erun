package deploy

import (
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
)

func TestSelectMCPIssuer(t *testing.T) {
	cases := []struct {
		name    string
		issuers []model.TenantIssuer
		want    string
	}{
		{name: "none", issuers: nil, want: ""},
		{
			name:    "file only (desktop) -> none",
			issuers: []model.TenantIssuer{{Issuer: "file:///etc/erun/mcp-auth/desktopid.pub"}},
			want:    "",
		},
		{
			name:    "https OIDC issuer",
			issuers: []model.TenantIssuer{{Issuer: "https://issuer.example"}},
			want:    "https://issuer.example",
		},
		{
			name:    "http OIDC issuer (local) qualifies",
			issuers: []model.TenantIssuer{{Issuer: "http://localhost:8080"}},
			want:    "http://localhost:8080",
		},
		{
			name: "skips file:// and returns the OIDC issuer",
			issuers: []model.TenantIssuer{
				{Issuer: "file:///etc/erun/mcp-auth/desktopid.pub"},
				{Issuer: "https://issuer.example"},
			},
			want: "https://issuer.example",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := selectMCPIssuer(tc.issuers); got != tc.want {
				t.Errorf("selectMCPIssuer = %q, want %q", got, tc.want)
			}
		})
	}
}
