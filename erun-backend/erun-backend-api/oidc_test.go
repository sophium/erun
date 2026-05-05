package backendapi

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/identitystore"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
	ssoadmintypes "github.com/aws/aws-sdk-go-v2/service/ssoadmin/types"
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
	r := &awsIdentityCenterUsernameResolver{identityStoreID: "d-1234567890"}
	username, err := r.ResolveUsername(context.Background(), UsernameResolutionClaims{
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
	r := &awsIdentityCenterUsernameResolver{
		identityStoreID: "d-1234567890",
		identityStore:   &mockIdentityStoreClient{username: "rihards.freimanis@example.com"},
	}
	username, err := r.ResolveUsername(context.Background(), UsernameResolutionClaims{
		AWSIdentityStoreUserID: "265222f4-f041-7008-6e0c-2d3993b555bf",
		AWSSourceRegion:        "eu-west-2",
	})
	if err != nil {
		t.Fatalf("ResolveUsername failed: %v", err)
	}
	if username != "rihards.freimanis@example.com" {
		t.Fatalf("unexpected username: %q", username)
	}
}

func TestAWSIdentityCenterUsernameResolverDiscoversIdentityStoreID(t *testing.T) {
	r := &awsIdentityCenterUsernameResolver{
		ssoAdmin:      &mockSSOAdminClient{identityStoreID: "d-1234567890"},
		identityStore: &mockIdentityStoreClient{username: "Rihards.Freimanis"},
	}
	username, err := r.ResolveUsername(context.Background(), UsernameResolutionClaims{
		AWSIdentityStoreUserID: "265222f4-f041-7008-6e0c-2d3993b555bf",
		AWSSourceRegion:        "eu-west-2",
	})
	if err != nil {
		t.Fatalf("ResolveUsername failed: %v", err)
	}
	if username != "Rihards.Freimanis" {
		t.Fatalf("unexpected username: %q", username)
	}
}

func TestNewOIDCTokenVerifierWithOptionsConfiguresAWSUsernameResolver(t *testing.T) {
	verifier := NewOIDCTokenVerifierWithOptions(OIDCTokenVerifierOptions{})
	resolver, ok := verifier.usernameResolver.(*awsIdentityCenterUsernameResolver)
	if !ok {
		t.Fatalf("expected AWS Identity Center username resolver, got %T", verifier.usernameResolver)
	}
	if resolver.identityStoreID != "" {
		t.Fatalf("unexpected identity store id: %q", resolver.identityStoreID)
	}
}

type mockSSOAdminClient struct {
	identityStoreID string
	err             error
}

func (m *mockSSOAdminClient) ListInstances(ctx context.Context, params *ssoadmin.ListInstancesInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.ListInstancesOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &ssoadmin.ListInstancesOutput{
		Instances: []ssoadmintypes.InstanceMetadata{
			{IdentityStoreId: aws.String(m.identityStoreID)},
		},
	}, nil
}

type mockIdentityStoreClient struct {
	username string
	err      error
}

func (m *mockIdentityStoreClient) DescribeUser(ctx context.Context, params *identitystore.DescribeUserInput, optFns ...func(*identitystore.Options)) (*identitystore.DescribeUserOutput, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &identitystore.DescribeUserOutput{
		UserName: aws.String(m.username),
	}, nil
}
