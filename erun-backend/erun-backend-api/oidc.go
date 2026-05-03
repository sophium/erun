package backendapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
)

type OIDCTokenVerifier struct {
	allowedIssuers   map[string]struct{}
	providers        map[string]*oidc.Provider
	verifiers        map[string]*oidc.IDTokenVerifier
	usernameResolver UsernameResolver
	mu               sync.Mutex
}

type oidcTokenClaims struct {
	Issuer            string       `json:"iss"`
	Subject           string       `json:"sub"`
	PreferredUsername string       `json:"preferred_username"`
	Username          string       `json:"username"`
	Email             string       `json:"email"`
	AWS               awsSTSClaims `json:"https://sts.amazonaws.com/"`
}

type awsSTSClaims struct {
	IdentityStoreUserID string `json:"identity_store_user_id"`
	SourceRegion        string `json:"source_region"`
}

type OIDCTokenVerifierOptions struct {
	AllowedIssuers     []string
	AWSIdentityStoreID string
	AWSRegion          string
	AWSCLIPath         string
	UsernameResolver   UsernameResolver
}

type UsernameResolver interface {
	ResolveUsername(ctx context.Context, claims UsernameResolutionClaims) (string, error)
}

type UsernameResolutionClaims struct {
	Issuer                 string
	Subject                string
	AWSIdentityStoreUserID string
	AWSSourceRegion        string
}

func NewOIDCTokenVerifier(allowedIssuers []string) *OIDCTokenVerifier {
	return NewOIDCTokenVerifierWithOptions(OIDCTokenVerifierOptions{AllowedIssuers: allowedIssuers})
}

func NewOIDCTokenVerifierWithOptions(options OIDCTokenVerifierOptions) *OIDCTokenVerifier {
	allowed := make(map[string]struct{}, len(options.AllowedIssuers))
	for _, issuer := range options.AllowedIssuers {
		if issuer = strings.TrimSpace(issuer); issuer != "" {
			allowed[issuer] = struct{}{}
		}
	}
	usernameResolver := options.UsernameResolver
	if usernameResolver == nil {
		usernameResolver = AWSIdentityCenterUsernameResolver{
			IdentityStoreID: options.AWSIdentityStoreID,
			Region:          options.AWSRegion,
			AWSCLIPath:      options.AWSCLIPath,
		}
	}
	return &OIDCTokenVerifier{
		allowedIssuers:   allowed,
		providers:        make(map[string]*oidc.Provider),
		verifiers:        make(map[string]*oidc.IDTokenVerifier),
		usernameResolver: usernameResolver,
	}
}

func (v *OIDCTokenVerifier) VerifyBearerToken(ctx context.Context, token string) (Claims, error) {
	issuer, err := issuerFromJWT(token)
	if err != nil {
		return Claims{}, err
	}
	if len(v.allowedIssuers) > 0 {
		if _, ok := v.allowedIssuers[issuer]; !ok {
			return Claims{}, fmt.Errorf("oidc issuer is not allowed: %s", issuer)
		}
	}

	verifier, err := v.verifier(ctx, issuer)
	if err != nil {
		return Claims{}, err
	}
	idToken, err := verifier.Verify(ctx, token)
	if err != nil {
		return Claims{}, err
	}

	var claims oidcTokenClaims
	if err := idToken.Claims(&claims); err != nil {
		return Claims{}, err
	}
	claims = applyUsernameResolution(ctx, v.usernameResolver, claims)
	return claimsFromOIDCTokenClaims(claims), nil
}

func applyUsernameResolution(ctx context.Context, resolver UsernameResolver, claims oidcTokenClaims) oidcTokenClaims {
	if resolver == nil {
		return claims
	}
	username, err := resolver.ResolveUsername(ctx, usernameResolutionClaims(claims))
	if err != nil {
		log.Printf("erun api auth username resolution skipped issuer=%q subject=%q reason=%q", claims.Issuer, claims.Subject, err.Error())
		return claims
	}
	if username = strings.TrimSpace(username); username != "" {
		claims.PreferredUsername = username
		claims.Username = username
	}
	return claims
}

func usernameResolutionClaims(claims oidcTokenClaims) UsernameResolutionClaims {
	return UsernameResolutionClaims{
		Issuer:                 strings.TrimSpace(claims.Issuer),
		Subject:                strings.TrimSpace(claims.Subject),
		AWSIdentityStoreUserID: strings.TrimSpace(claims.AWS.IdentityStoreUserID),
		AWSSourceRegion:        strings.TrimSpace(claims.AWS.SourceRegion),
	}
}

func claimsFromOIDCTokenClaims(claims oidcTokenClaims) Claims {
	username := strings.TrimSpace(claims.PreferredUsername)
	if username == "" {
		username = strings.TrimSpace(claims.Username)
	}
	if username == "" {
		username = strings.TrimSpace(claims.Email)
	}
	subject := strings.TrimSpace(claims.Subject)
	if identityStoreUserID := strings.TrimSpace(claims.AWS.IdentityStoreUserID); identityStoreUserID != "" {
		subject = identityStoreUserID
	}
	return Claims{
		Issuer:   strings.TrimSpace(claims.Issuer),
		Subject:  subject,
		Username: username,
	}
}

func (v *OIDCTokenVerifier) verifier(ctx context.Context, issuer string) (*oidc.IDTokenVerifier, error) {
	v.mu.Lock()
	if verifier := v.verifiers[issuer]; verifier != nil {
		v.mu.Unlock()
		return verifier, nil
	}
	v.mu.Unlock()

	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	verifier := provider.Verifier(&oidc.Config{SkipClientIDCheck: true})

	v.mu.Lock()
	v.providers[issuer] = provider
	v.verifiers[issuer] = verifier
	v.mu.Unlock()
	return verifier, nil
}

type AWSIdentityCenterUsernameResolver struct {
	IdentityStoreID string
	Region          string
	AWSCLIPath      string
}

func (r AWSIdentityCenterUsernameResolver) ResolveUsername(ctx context.Context, claims UsernameResolutionClaims) (string, error) {
	userID := strings.TrimSpace(claims.AWSIdentityStoreUserID)
	if userID == "" {
		return "", nil
	}
	region := strings.TrimSpace(r.Region)
	if region == "" {
		region = strings.TrimSpace(claims.AWSSourceRegion)
	}
	awsCLI := strings.TrimSpace(r.AWSCLIPath)
	if awsCLI == "" {
		awsCLI = "aws"
	}
	identityStoreID := strings.TrimSpace(r.IdentityStoreID)
	if identityStoreID == "" {
		var err error
		identityStoreID, err = r.resolveIdentityStoreID(ctx, region, awsCLI)
		if err != nil {
			return "", err
		}
		if identityStoreID == "" {
			return "", nil
		}
	}

	args := []string{
		"identitystore", "describe-user",
		"--identity-store-id", identityStoreID,
		"--user-id", userID,
		"--query", "UserName",
		"--output", "text",
	}
	if region != "" {
		args = append(args, "--region", region)
	}
	username, err := runAWSCLIText(ctx, awsCLI, args...)
	if err != nil {
		return "", fmt.Errorf("resolve AWS Identity Center username: %s", err)
	}
	if username == "None" || username == "null" {
		return "", nil
	}
	return username, nil
}

func (r AWSIdentityCenterUsernameResolver) resolveIdentityStoreID(ctx context.Context, region string, awsCLI string) (string, error) {
	args := []string{
		"sso-admin", "list-instances",
		"--query", "Instances[0].IdentityStoreId",
		"--output", "text",
	}
	if region != "" {
		args = append(args, "--region", region)
	}
	identityStoreID, err := runAWSCLIText(ctx, awsCLI, args...)
	if err != nil {
		return "", fmt.Errorf("resolve AWS Identity Center identity store id: %s", err)
	}
	if identityStoreID == "None" || identityStoreID == "null" {
		return "", nil
	}
	return identityStoreID, nil
}

func runAWSCLIText(ctx context.Context, awsCLI string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, awsCLI, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return "", errors.New(message)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func issuerFromJWT(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return "", errors.New("token is not a jwt")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	var claims oidcTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", err
	}
	issuer := strings.TrimSpace(claims.Issuer)
	if issuer == "" {
		return "", errors.New("token issuer is empty")
	}
	return issuer, nil
}
