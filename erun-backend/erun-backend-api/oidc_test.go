package backendapi

import "testing"

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
