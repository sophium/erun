package backendapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/identitystore"
	"github.com/aws/aws-sdk-go-v2/service/ssoadmin"
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
		usernameResolver = newAWSIdentityCenterUsernameResolver(options.AWSIdentityStoreID, options.AWSRegion)
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

type ssoadminClient interface {
	ListInstances(ctx context.Context, params *ssoadmin.ListInstancesInput, optFns ...func(*ssoadmin.Options)) (*ssoadmin.ListInstancesOutput, error)
}

type identitystoreClient interface {
	DescribeUser(ctx context.Context, params *identitystore.DescribeUserInput, optFns ...func(*identitystore.Options)) (*identitystore.DescribeUserOutput, error)
}

type awsIdentityCenterUsernameResolver struct {
	identityStoreID string
	ssoAdmin        ssoadminClient
	identityStore   identitystoreClient

	resolveOnce             sync.Once
	resolvedIdentityStoreID string
	resolveErr              error
}

func newAWSIdentityCenterUsernameResolver(identityStoreID, region string) *awsIdentityCenterUsernameResolver {
	cfg, err := config.LoadDefaultConfig(context.Background())
	if err != nil {
		// Return a resolver that will fail on first use with this error.
		return &awsIdentityCenterUsernameResolver{
			identityStoreID: identityStoreID,
			resolveErr:      fmt.Errorf("load AWS config: %w", err),
		}
	}
	if region != "" {
		cfg.Region = region
	}
	return &awsIdentityCenterUsernameResolver{
		identityStoreID: identityStoreID,
		ssoAdmin:        ssoadmin.NewFromConfig(cfg),
		identityStore:   identitystore.NewFromConfig(cfg),
	}
}

func (r *awsIdentityCenterUsernameResolver) ResolveUsername(ctx context.Context, claims UsernameResolutionClaims) (string, error) {
	userID := strings.TrimSpace(claims.AWSIdentityStoreUserID)
	if userID == "" {
		return "", nil
	}

	identityStoreID, err := r.getIdentityStoreID(ctx)
	if err != nil {
		return "", err
	}
	if identityStoreID == "" {
		return "", nil
	}

	out, err := r.identityStore.DescribeUser(ctx, &identitystore.DescribeUserInput{
		IdentityStoreId: aws.String(identityStoreID),
		UserId:          aws.String(userID),
	})
	if err != nil {
		return "", fmt.Errorf("resolve AWS Identity Center username: %w", err)
	}
	return aws.ToString(out.UserName), nil
}

// getIdentityStoreID returns the configured identity store ID, resolving it from
// AWS SSO Admin on first call when it was not provided at construction time.
func (r *awsIdentityCenterUsernameResolver) getIdentityStoreID(ctx context.Context) (string, error) {
	if r.identityStoreID != "" {
		return r.identityStoreID, nil
	}
	r.resolveOnce.Do(func() {
		if r.resolveErr != nil {
			return
		}
		out, err := r.ssoAdmin.ListInstances(ctx, &ssoadmin.ListInstancesInput{})
		if err != nil {
			r.resolveErr = fmt.Errorf("resolve AWS Identity Center identity store id: %w", err)
			return
		}
		if len(out.Instances) > 0 {
			r.resolvedIdentityStoreID = aws.ToString(out.Instances[0].IdentityStoreId)
		}
	})
	return r.resolvedIdentityStoreID, r.resolveErr
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
