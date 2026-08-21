package eruncommon

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
)

// MCP auth edge. The per-env erun-mcp server is publicly exposed and its `raw`
// tool can kubectl-exec, so every call must be authenticated. A trusted issuer
// is one of two kinds, dispatched on its scheme:
//
//   - `file://<path>` — desktop identity. EdDSA only: `alg` is hard-checked, so
//     the alg-confusion class (accepting `none`/HMAC/RS256 against a public key)
//     cannot occur.
//   - `https://…` — an OIDC issuer, verified against its published JWKS; standard
//     OIDC signing algorithms apply, so the EdDSA alg-lock does not.
//
// Security invariant, both branches: the key/issuer trusted is only ever the one
// the caller configured, never an arbitrary issuer taken from the token. The
// tenant is resolved from the trusted-issuer→tenant map by the caller (erun-mcp).

// MCPTokenClaims is the registered-claim subset the MCP auth edge uses.
type MCPTokenClaims struct {
	Issuer    string `json:"iss"`
	Subject   string `json:"sub,omitempty"`
	Audience  string `json:"aud,omitempty"`
	IssuedAt  int64  `json:"iat,omitempty"`
	ExpiresAt int64  `json:"exp,omitempty"`
	// Scope and Roles carry what the caller may do, in the two shapes issuers
	// actually produce: a space-delimited OAuth scope string, and a roles array
	// of the kind project roles arrive in. Both are optional — a token carrying
	// neither is the desktop's single-admin case.
	Scope string   `json:"scope,omitempty"`
	Roles []string `json:"roles,omitempty"`
}

// Capabilities resolves what this token permits at the edge.
func (c MCPTokenClaims) Capabilities() MCPCapabilitySet {
	return MCPCapabilitiesFromClaims(c.Scope, c.Roles)
}

// Only EdDSA is ever produced or accepted.
type mcpTokenHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

const mcpTokenAlgorithm = "EdDSA"

// GenerateDesktopIdentity creates a new Ed25519 desktop identity keypair,
// PEM-encoded (PKCS#8 private, PKIX public). It is the desktop's trust anchor:
// the private key signs MCP tokens, the public key is injected into the runtime
// pod for verification.
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

// FileIssuer formats a public-key path as the `file://` issuer the desktop
// stamps in the token's `iss` claim and the MCP server is configured to trust.
// The path is rendered as a valid RFC 8089 file URL on every OS: a Windows drive
// path (C:\dir\key.pub) must become file:///C:/dir/key.pub, or url.Parse fails to
// recognize the file scheme and verification falls through to the OIDC path.
func FileIssuer(publicKeyPath string) string {
	return fileIssuerScheme + fileURLPath(publicKeyPath)
}

// fileIssuerScheme distinguishes an issuer backed by a local public key from an
// OIDC issuer the edge fetches a JWKS from; the two have different recoveries
// when a deploy would drop authentication.
const fileIssuerScheme = "file://"

// fileURLPath renders an OS path as the path component of a file:// URL:
// forward-slashed with a leading slash, so a Windows drive path (C:\dir) becomes
// /C:/dir. A Unix absolute path already starts with a slash and is returned
// unchanged, keeping the in-pod issuer file:///etc/erun/mcp-auth/desktopid.pub.
func fileURLPath(p string) string {
	p = filepath.ToSlash(p)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return p
}

// fileURLPathToOSPath is the inverse of fileURLPath: it turns a file:// URL path
// back into an OS path so the key file can be read. On Windows the path arrives as
// /C:/dir/key.pub; the leading slash before the drive letter is dropped and the
// separators are flipped back.
func fileURLPathToOSPath(p string) string {
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' &&
		((p[1] >= 'A' && p[1] <= 'Z') || (p[1] >= 'a' && p[1] <= 'z')) {
		p = p[1:]
	}
	return filepath.FromSlash(p)
}

const (
	// DesktopMCPPublicKeyDir is the in-pod directory the local-key signer's
	// public key is mounted into by the runtime chart — the desktop's own key
	// for a desktop deploy, or the hosted backend's MCP-signing public key for
	// a hosted deploy (#1084).
	DesktopMCPPublicKeyDir  = "/etc/erun/mcp-auth"
	desktopMCPPublicKeyFile = "desktopid.pub"
)

// DesktopMCPPublicKeyPath is the in-pod path the local-key signer's public key
// is mounted at — the single location the chart mount, the signer's `iss`
// claim, and the server's trusted-issuer env all derive from, so they cannot
// drift.
func DesktopMCPPublicKeyPath() string {
	return DesktopMCPPublicKeyDir + "/" + desktopMCPPublicKeyFile
}

// DesktopMCPIssuer is the fixed `file://` issuer every local-key-signed MCP
// token carries and the MCP server is configured to trust: the desktop signs
// with it directly, and the hosted backend signs with it too when minting
// per-env tokens on the console's behalf (mcptoken.Signer) — same mechanism,
// two signers, one issuer. Despite the name, this is not desktop-only.
func DesktopMCPIssuer() string {
	return FileIssuer(DesktopMCPPublicKeyPath())
}

// MCPTokenAudience is the stable per-environment audience a token must carry and
// the env's MCP edge enforces, so a token minted for one environment cannot be
// replayed against another. The value is transport-independent, so the signer
// and the chart's ERUN_MCP_AUDIENCE always agree.
func MCPTokenAudience(tenant, environment string) string {
	return "erun-mcp:" + strings.TrimSpace(tenant) + "/" + strings.TrimSpace(environment)
}

// SignMCPToken signs an EdDSA (Ed25519) JWT for the given claims with the
// PEM-encoded private key. The claims' Issuer must be a `file://<path>` URL
// naming the public-key file the verifier will load.
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

// VerifyMCPToken verifies an MCP bearer token against trustedIssuer — the issuer
// the MCP server is configured to trust, never an arbitrary issuer from the
// token — dispatching file:// to Ed25519 local-key verification (used by both
// the desktop and a hosted backend's signer) and https:// to OIDC JWKS
// verification.
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
	// Only ever load the key from the issuer we already trust — never a
	// file:// path supplied by the (unverified) token.
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

// The OIDC verifier already checks the signature and time claims; the expiry is
// re-checked here only for parity with the file:// path.
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
	// An OIDC `aud` may list multiple audiences; the per-env audience must be one
	// of them — the same membership contract the file:// path enforces.
	if err := validateMCPAudience(verified.Audience, expectedAudience); err != nil {
		return MCPTokenClaims{}, err
	}
	return claims, nil
}

// The single Audience field carries only the first audience as a human-readable
// hint; the authoritative multi-audience check stays in validateMCPAudience.
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

// Hard-requires the EdDSA algorithm in the header, closing the alg-confusion
// class (none / HMAC verified against the public-key bytes).
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

// An absent exp (0) is not enforced.
func validateMCPExpiry(expiresAt int64, now time.Time) error {
	if expiresAt != 0 && now.After(time.Unix(expiresAt, 0)) {
		return fmt.Errorf("MCP token expired")
	}
	return nil
}

// An empty expectedAudience disables the check: the chart did not pin an audience.
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

// UnverifiedMCPTokenIssuer extracts the `iss` claim WITHOUT verifying the
// signature — used only to select which trusted issuer (and tenant) the token
// claims to come from. VerifyMCPToken then re-checks the issuer and verifies the
// signature, so the per-issuer alg policy is enforced there, not here.
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

// DesktopPublicKeyPEM derives the PKIX public-key PEM from the private key, so
// the private key is the single thing the desktop persists and the public half
// is recomputed for injection on deploy.
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
