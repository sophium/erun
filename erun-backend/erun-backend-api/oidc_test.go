package backendapi

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestClaimsFromOIDCTokenClaimsUsesAWSIdentityStoreUserID(t *testing.T) {
	claims := claimsFromOIDCTokenClaims(oidcTokenClaims{
		Issuer:  "https://a11bec5a-678d-4a6a-aa25-f3770df2ac5e.tokens.sts.global.api.aws",
		Subject: "arn:aws:iam::020362606330:role/aws-reserved/sso.amazonaws.com/eu-west-2/AWSReservedSSO_AdministratorAccess_c95738f708c1c268",
		AWS: awsSTSClaims{
			IdentityStoreUserID: "265222f4-f041-7008-6e0c-2d3993b555bf",
		},
	})

	if claims.Subject != "265222f4-f041-7008-6e0c-2d3993b555bf" {
		t.Fatalf("expected AWS identity store user id as subject, got %q", claims.Subject)
	}
}

func TestClaimsFromOIDCTokenClaimsFallsBackToSubjectWithoutAWSIdentityStoreUserID(t *testing.T) {
	claims := claimsFromOIDCTokenClaims(oidcTokenClaims{
		Issuer:  "https://a11bec5a-678d-4a6a-aa25-f3770df2ac5e.tokens.sts.global.api.aws",
		Subject: "arn:aws:iam::020362606330:role/aws-reserved/sso.amazonaws.com/eu-west-2/AWSReservedSSO_AdministratorAccess_c95738f708c1c268",
	})

	if claims.Subject != "arn:aws:iam::020362606330:role/aws-reserved/sso.amazonaws.com/eu-west-2/AWSReservedSSO_AdministratorAccess_c95738f708c1c268" {
		t.Fatalf("expected subject fallback, got %q", claims.Subject)
	}
}

func TestClaimsFromOIDCTokenClaimsKeepsNonAWSSubject(t *testing.T) {
	claims := claimsFromOIDCTokenClaims(oidcTokenClaims{
		Issuer:            "https://issuer.example",
		Subject:           "user-1",
		PreferredUsername: "user@example",
	})

	if claims.Subject != "user-1" {
		t.Fatalf("expected standard OIDC subject, got %q", claims.Subject)
	}
	if claims.Username != "user@example" {
		t.Fatalf("expected preferred username, got %q", claims.Username)
	}
}

func TestApplyUsernameResolutionIgnoresResolverError(t *testing.T) {
	claims := applyUsernameResolution(context.Background(), failingUsernameResolver{}, oidcTokenClaims{
		Issuer:   "https://issuer.example",
		Subject:  "user-1",
		Username: "fallback",
	})

	if claims.Username != "fallback" {
		t.Fatalf("expected fallback username, got %q", claims.Username)
	}
}

type failingUsernameResolver struct{}

func (failingUsernameResolver) ResolveUsername(context.Context, UsernameResolutionClaims) (string, error) {
	return "", errors.New("missing aws credentials")
}

func TestAWSIdentityCenterUsernameResolverSkipsNonAWSTokens(t *testing.T) {
	username, err := (AWSIdentityCenterUsernameResolver{IdentityStoreID: "d-1234567890"}).ResolveUsername(context.Background(), UsernameResolutionClaims{
		Issuer:  "https://issuer.example",
		Subject: "user-1",
	})
	if err != nil {
		t.Fatalf("ResolveUsername failed: %v", err)
	}
	if username != "" {
		t.Fatalf("expected empty username for non-AWS token, got %q", username)
	}
}

func TestAWSIdentityCenterUsernameResolverUsesIdentityStoreUserID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper uses POSIX sh")
	}
	dir := t.TempDir()
	awsCLI := filepath.Join(dir, "aws")
	if err := os.WriteFile(awsCLI, []byte(`#!/bin/sh
printf '%s\n' "$*" > "$AWS_ARGS_FILE"
printf 'rihards.freimanis@example.com\n'
`), 0o755); err != nil {
		t.Fatalf("write aws helper: %v", err)
	}
	argsFile := filepath.Join(dir, "args")
	t.Setenv("AWS_ARGS_FILE", argsFile)

	username, err := (AWSIdentityCenterUsernameResolver{
		IdentityStoreID: "d-1234567890",
		AWSCLIPath:      awsCLI,
	}).ResolveUsername(context.Background(), UsernameResolutionClaims{
		AWSIdentityStoreUserID: "265222f4-f041-7008-6e0c-2d3993b555bf",
		AWSSourceRegion:        "eu-west-2",
	})
	if err != nil {
		t.Fatalf("ResolveUsername failed: %v", err)
	}
	if username != "rihards.freimanis@example.com" {
		t.Fatalf("unexpected username: %q", username)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read aws args: %v", err)
	}
	gotArgs := strings.TrimSpace(string(args))
	wantArgs := "identitystore describe-user --identity-store-id d-1234567890 --user-id 265222f4-f041-7008-6e0c-2d3993b555bf --query UserName --output text --region eu-west-2"
	if gotArgs != wantArgs {
		t.Fatalf("unexpected aws args:\nwant: %s\n got: %s", wantArgs, gotArgs)
	}
}

func TestAWSIdentityCenterUsernameResolverDiscoversIdentityStoreID(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell helper uses POSIX sh")
	}
	dir := t.TempDir()
	awsCLI := filepath.Join(dir, "aws")
	if err := os.WriteFile(awsCLI, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$AWS_ARGS_FILE"
case "$*" in
  sso-admin*) printf 'd-1234567890\n' ;;
  identitystore*) printf 'Rihards.Freimanis\n' ;;
  *) printf 'unexpected command\n' >&2; exit 1 ;;
esac
`), 0o755); err != nil {
		t.Fatalf("write aws helper: %v", err)
	}
	argsFile := filepath.Join(dir, "args")
	t.Setenv("AWS_ARGS_FILE", argsFile)

	username, err := (AWSIdentityCenterUsernameResolver{
		AWSCLIPath: awsCLI,
	}).ResolveUsername(context.Background(), UsernameResolutionClaims{
		AWSIdentityStoreUserID: "265222f4-f041-7008-6e0c-2d3993b555bf",
		AWSSourceRegion:        "eu-west-2",
	})
	if err != nil {
		t.Fatalf("ResolveUsername failed: %v", err)
	}
	if username != "Rihards.Freimanis" {
		t.Fatalf("unexpected username: %q", username)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read aws args: %v", err)
	}
	gotArgs := strings.TrimSpace(string(args))
	for _, want := range []string{
		"sso-admin list-instances --query Instances[0].IdentityStoreId --output text --region eu-west-2",
		"identitystore describe-user --identity-store-id d-1234567890 --user-id 265222f4-f041-7008-6e0c-2d3993b555bf --query UserName --output text --region eu-west-2",
	} {
		if !strings.Contains(gotArgs, want) {
			t.Fatalf("expected aws args to contain %q, got:\n%s", want, gotArgs)
		}
	}
}

func TestNewOIDCTokenVerifierWithOptionsConfiguresAWSUsernameResolver(t *testing.T) {
	verifier := NewOIDCTokenVerifierWithOptions(OIDCTokenVerifierOptions{})
	resolver, ok := verifier.usernameResolver.(AWSIdentityCenterUsernameResolver)
	if !ok {
		t.Fatalf("expected AWS Identity Center username resolver, got %T", verifier.usernameResolver)
	}
	if resolver.IdentityStoreID != "" {
		t.Fatalf("unexpected identity store id: %q", resolver.IdentityStoreID)
	}
}
