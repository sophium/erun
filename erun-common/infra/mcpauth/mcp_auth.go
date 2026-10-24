package mcpauth

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/infra/mcptools"
)

// MCP auth edge. The edge is public and can kubectl-exec, so every call is authenticated.
// file:// issuers are EdDSA-only (no alg confusion); https:// issuers verify via OIDC JWKS.
// Only the caller-configured issuer is ever trusted, never one taken from the token.

// MCPTokenClaims is the claim subset the MCP auth edge uses.
type MCPTokenClaims struct {
	Issuer    string `json:"iss"`
	Subject   string `json:"sub,omitempty"`
	Audience  string `json:"aud,omitempty"`
	IssuedAt  int64  `json:"iat,omitempty"`
	ExpiresAt int64  `json:"exp,omitempty"`
	// A token with neither Scope nor Roles is the desktop's single-admin case.
	Scope string   `json:"scope,omitempty"`
	Roles []string `json:"roles,omitempty"`
}

// Capabilities resolves what the token permits at the edge.
func (c MCPTokenClaims) Capabilities() mcptools.MCPCapabilitySet {
	return mcptools.MCPCapabilitiesFromClaims(c.Scope, c.Roles)
}

type mcpTokenHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

const mcpTokenAlgorithm = "EdDSA"

// GenerateDesktopIdentity creates the desktop's Ed25519 trust anchor as PKCS#8/PKIX PEM.
func GenerateDesktopIdentity() (privatePEM, publicPEM []byte, err error) {
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, nil, fmt.Errorf("generate desktop identity: %w", err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal desktop private key: %w", err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal desktop public key: %w", err)
	}
	privatePEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	publicPEM = pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})
	return privatePEM, publicPEM, nil
}

// FileIssuer renders a public-key path as an RFC 8089 file URL; a Windows drive path must
// become file:///C:/... or url.Parse misses the scheme.
func FileIssuer(publicKeyPath string) string {
	return FileIssuerScheme + fileURLPath(publicKeyPath)
}

// FileIssuerScheme marks an issuer backed by a local public key rather than OIDC.
const FileIssuerScheme = "file://"

// fileURLPath forward-slashes an OS path with a leading slash (C:\dir -> /C:/dir).
func fileURLPath(p string) string {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// fileURLPathToOSPath inverts fileURLPath.
func fileURLPathToOSPath(p string) string {
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' &&
		((p[1] >= 'A' && p[1] <= 'Z') || (p[1] >= 'a' && p[1] <= 'z')) {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

const (
	// DesktopMCPPublicKeyDir holds the local-key signer's public key, desktop or hosted backend.
	DesktopMCPPublicKeyDir  = "/etc/erun/mcp-auth"
	DesktopMCPPublicKeyFile = "desktopid.pub"
)

// DesktopMCPPublicKeyPath is the one path the chart mount, `iss` claim, and trusted issuer derive from.
func DesktopMCPPublicKeyPath() string {
	return DesktopMCPPublicKeyDir + "/" + DesktopMCPPublicKeyFile
}

// DesktopMCPIssuer is the file:// issuer for every local-key-signed token, desktop or hosted backend.
func DesktopMCPIssuer() string {
	return FileIssuer(DesktopMCPPublicKeyPath())
}

// MCPTokenAudience is per environment so a token cannot be replayed against another environment.
func MCPTokenAudience(tenant, environment string) string {
	return "erun-mcp:" + strings.TrimSpace(tenant) + "/" + strings.TrimSpace(environment)
}

// SignMCPToken signs an EdDSA JWT; claims.Issuer must be the file:// URL of the verifier's public key.
func SignMCPToken(privatePEM []byte, claims MCPTokenClaims) (string, error) {
	key, err := parseEd25519PrivateKey(privatePEM)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(claims.Issuer) == "" {
		return "", fmt.Errorf("mcp token issuer is required")
	}
	headerSegment, err := encodeJWTSegment(mcpTokenHeader{Algorithm: mcpTokenAlgorithm, Type: "JWT"})
	if err != nil {
		return "", err
	}
	claimsSegment, err := encodeJWTSegment(claims)
	if err != nil {
		return "", err
	}
	signingInput := headerSegment + "." + claimsSegment
	signature := ed25519.Sign(key, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

// VerifyMCPToken verifies a bearer against trustedIssuer: file:// via Ed25519, https:// via OIDC.
func VerifyMCPToken(ctx context.Context, oidc *OIDCVerifier, token, trustedIssuer, expectedAudience string, now time.Time) (MCPTokenClaims, error) {
	trustedIssuer = strings.TrimSpace(trustedIssuer)
	if trustedIssuer == "" {
		return MCPTokenClaims{}, fmt.Errorf("no trusted issuer configured for MCP auth")
	}
	if isFileIssuer(trustedIssuer) {
		return verifyFileMCPToken(token, trustedIssuer, expectedAudience, now)
	}
	return verifyOIDCMCPToken(ctx, oidc, token, trustedIssuer, expectedAudience, now)
}

func isFileIssuer(issuer string) bool {
	parsed, err := url.Parse(issuer)
	return err == nil && parsed.Scheme == "file"
}

func verifyFileMCPToken(token, trustedIssuer, expectedAudience string, now time.Time) (MCPTokenClaims, error) {
	signingInput, claims, signature, err := parseMCPToken(token)
	if err != nil {
		return MCPTokenClaims{}, err
	}
	// Never load a key from a path the unverified token supplies.
	if claims.Issuer != trustedIssuer {
		return MCPTokenClaims{}, fmt.Errorf("MCP token issuer %q is not the trusted issuer", claims.Issuer)
	}
	publicKey, err := loadEd25519PublicKeyFromFileIssuer(trustedIssuer)
	if err != nil {
		return MCPTokenClaims{}, err
	}
	if !ed25519.Verify(publicKey, []byte(signingInput), signature) {
		return MCPTokenClaims{}, fmt.Errorf("MCP token signature is not valid for the trusted public key")
	}
	if err := validateMCPExpiry(claims.ExpiresAt, now); err != nil {
		return MCPTokenClaims{}, err
	}
	if err := validateMCPAudience([]string{claims.Audience}, expectedAudience); err != nil {
		return MCPTokenClaims{}, err
	}
	return claims, nil
}

// Expiry is re-checked only for parity with the file:// path.
func verifyOIDCMCPToken(ctx context.Context, oidc *OIDCVerifier, token, trustedIssuer, expectedAudience string, now time.Time) (MCPTokenClaims, error) {
	if oidc == nil {
		return MCPTokenClaims{}, fmt.Errorf("no OIDC verifier configured for issuer %q", trustedIssuer)
	}
	verified, err := oidc.Verify(ctx, trustedIssuer, token)
	if err != nil {
		return MCPTokenClaims{}, err
	}
	if verified.Issuer != trustedIssuer {
		return MCPTokenClaims{}, fmt.Errorf("MCP token issuer %q is not the trusted issuer", verified.Issuer)
	}
	claims := mcpClaimsFromOIDC(verified)
	if err := validateMCPExpiry(claims.ExpiresAt, now); err != nil {
		return MCPTokenClaims{}, err
	}
	if err := validateMCPAudience(verified.Audience, expectedAudience); err != nil {
		return MCPTokenClaims{}, err
	}
	return claims, nil
}

// Audience holds only the first audience as a hint; validateMCPAudience is authoritative.
func mcpClaimsFromOIDC(verified OIDCClaims) MCPTokenClaims {
	claims := MCPTokenClaims{Issuer: verified.Issuer, Subject: verified.Subject}
	if exp, ok := verified.Raw["exp"].(float64); ok {
		claims.ExpiresAt = int64(exp)
	}
	if len(verified.Audience) > 0 {
		claims.Audience = verified.Audience[0]
	}
	if scope, ok := verified.Raw["scope"].(string); ok {
		claims.Scope = scope
	}
	if roles, ok := verified.Raw["roles"].([]any); ok {
		for _, role := range roles {
			if name, ok := role.(string); ok {
				claims.Roles = append(claims.Roles, name)
			}
		}
	}
	return claims
}

// Requiring EdDSA closes the alg-confusion class (none/HMAC against the public key).
func parseMCPToken(token string) (signingInput string, claims MCPTokenClaims, signature []byte, err error) {
	segments := strings.Split(strings.TrimSpace(token), ".")
	if len(segments) != 3 {
		return "", MCPTokenClaims{}, nil, fmt.Errorf("malformed MCP token: expected 3 JWT segments")
	}
	var header mcpTokenHeader
	if err := decodeJWTSegment(segments[0], &header); err != nil {
		return "", MCPTokenClaims{}, nil, fmt.Errorf("decode MCP token header: %w", err)
	}
	if header.Algorithm != mcpTokenAlgorithm {
		return "", MCPTokenClaims{}, nil, fmt.Errorf("unsupported MCP token algorithm %q (only %s)", header.Algorithm, mcpTokenAlgorithm)
	}
	if err := decodeJWTSegment(segments[1], &claims); err != nil {
		return "", MCPTokenClaims{}, nil, fmt.Errorf("decode MCP token claims: %w", err)
	}
	signature, err = base64.RawURLEncoding.DecodeString(segments[2])
	if err != nil {
		return "", MCPTokenClaims{}, nil, fmt.Errorf("decode MCP token signature: %w", err)
	}
	return segments[0] + "." + segments[1], claims, signature, nil
}

func validateMCPExpiry(expiresAt int64, now time.Time) error {
	if expiresAt != 0 && now.After(time.Unix(expiresAt, 0)) {
		return fmt.Errorf("MCP token expired")
	}
	return nil
}

// An empty expectedAudience means the chart pinned none.
func validateMCPAudience(audiences []string, expectedAudience string) error {
	if expectedAudience == "" {
		return nil
	}
	for _, audience := range audiences {
		if audience == expectedAudience {
			return nil
		}
	}
	return fmt.Errorf("MCP token audience %v does not contain %q", audiences, expectedAudience)
}

// UnverifiedMCPTokenIssuer reads `iss` without verifying, only to pick the trusted issuer; VerifyMCPToken re-checks it.
func UnverifiedMCPTokenIssuer(token string) (string, error) {
	return IssuerFromUnverifiedJWT(token)
}

func loadEd25519PublicKeyFromFileIssuer(issuer string) (ed25519.PublicKey, error) {
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Scheme != "file" {
		return nil, fmt.Errorf("trusted issuer %q is not a file:// URL", issuer)
	}
	path := parsed.Path
	if path == "" {
		path = parsed.Opaque
	}
	if parsed.Host != "" {
		path = parsed.Host + path
	}
	path = fileURLPathToOSPath(path)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read MCP trusted public key %s: %w", path, err)
	}
	return parseEd25519PublicKey(data)
}

// DesktopPublicKeyPEM derives the public half so only the private key is persisted.
func DesktopPublicKeyPEM(privatePEM []byte) ([]byte, error) {
	key, err := parseEd25519PrivateKey(privatePEM)
	if err != nil {
		return nil, err
	}
	publicDER, err := x509.MarshalPKIXPublicKey(key.Public())
	if err != nil {
		return nil, fmt.Errorf("marshal desktop public key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), nil
}

func parseEd25519PrivateKey(privatePEM []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(privatePEM)
	if block == nil {
		return nil, fmt.Errorf("desktop private key is not valid PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse desktop private key: %w", err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("desktop private key is not an Ed25519 key")
	}
	return key, nil
}

func parseEd25519PublicKey(publicPEM []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(publicPEM)
	if block == nil {
		return nil, fmt.Errorf("trusted public key is not valid PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse trusted public key: %w", err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("trusted public key is not an Ed25519 key")
	}
	return key, nil
}

func encodeJWTSegment(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeJWTSegment(segment string, into any) error {
	decoded, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return err
	}
	return json.Unmarshal(decoded, into)
}
