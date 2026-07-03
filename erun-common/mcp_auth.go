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
	"strings"
	"time"
)

// MCP auth edge. The per-env erun-mcp server is exposed
// publicly and its `raw` tool can kubectl-exec, so it must always be
// authenticated. A trusted issuer is one of two kinds, dispatched on its scheme:
//
//   - `file://<path>` — the desktop case. The trust anchor is a
//     self-contained Ed25519 keypair: the desktop signs an EdDSA JWT with its
//     private key (desktopid.key), injects the matching public key into the
//     runtime pod, and names that public key in the token's `iss` claim as a
//     `file://<path>` URL. The verifier loads the public key from that path and
//     verifies the signature. EdDSA only — `alg` is hard-checked, so the JWT
//     alg-confusion class (accepting `none`/HMAC/RS256 against a public key)
//     cannot occur — and the verifier only ever loads the public key from the
//     issuer the caller already trusts, never an arbitrary `file://` from the
//     token.
//
//   - `https://…` — an OIDC issuer, e.g. a Zitadel or AWS STS issuer.
//     The signature is verified against the issuer's published JWKS via the
//     shared *OIDCVerifier (the same verifier the hosted backend API uses), and
//     `iss`/`exp`/audience are enforced. Standard OIDC signing algorithms apply
//     (RS256/ES256/…), so the EdDSA alg-lock does NOT apply to this branch.
//
// In both branches the verifier only ever trusts the issuer the caller already
// configured, the same per-env audience contract is enforced, and the tenant is
// resolved from the trusted-issuer→tenant map by the caller (erun-mcp).

// MCPTokenClaims is the registered-claim subset the MCP auth edge uses.
type MCPTokenClaims struct {
	Issuer    string `json:"iss"`
	Subject   string `json:"sub,omitempty"`
	Audience  string `json:"aud,omitempty"`
	IssuedAt  int64  `json:"iat,omitempty"`
	ExpiresAt int64  `json:"exp,omitempty"`
}

// mcpTokenHeader is the JWS header. Only EdDSA is ever produced or accepted.
type mcpTokenHeader struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
}

const mcpTokenAlgorithm = "EdDSA"

// GenerateDesktopIdentity creates a new Ed25519 desktop identity keypair and
// returns it PEM-encoded (PKCS#8 private, PKIX public). The desktop persists the
// private key as desktopid.key and injects the public key into the runtime pod;
// the MCP server verifies bearer tokens against the public key.
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

// FileIssuer formats a public-key file path as the `file://<path>` issuer URL
// the desktop puts in the token's `iss` claim and the MCP server is configured
// to trust. The path is the location of the public key inside the runtime pod.
func FileIssuer(publicKeyPath string) string {
	return "file://" + publicKeyPath
}

const (
	// DesktopMCPPublicKeyDir is the in-pod directory the desktop's public key is
	// mounted into by the runtime chart.
	DesktopMCPPublicKeyDir  = "/etc/erun/mcp-auth"
	desktopMCPPublicKeyFile = "desktopid.pub"
)

// DesktopMCPPublicKeyPath is the in-pod path the desktop public key is mounted
// at. It is the single location the `file://` issuer references and the MCP
// server loads the trusted key from, so the chart mount, the desktop signer's
// `iss` claim, and the server's trusted-issuer env all derive from it.
func DesktopMCPPublicKeyPath() string {
	return DesktopMCPPublicKeyDir + "/" + desktopMCPPublicKeyFile
}

// DesktopMCPIssuer is the `file://` issuer the desktop stamps in its tokens and
// the MCP server is configured to trust for a desktop deployment.
func DesktopMCPIssuer() string {
	return FileIssuer(DesktopMCPPublicKeyPath())
}

// MCPTokenAudience is the stable per-environment audience a desktop or console
// token must carry and the env's MCP edge enforces, so a token minted for one
// environment cannot be replayed against another. It is transport-
// independent — the same value whether the edge is reached over the desktop's
// local port-forward or the public Traefik route — so the signer and the
// chart's ERUN_MCP_AUDIENCE always agree.
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

// VerifyMCPToken verifies an MCP bearer token against trustedIssuer (the issuer
// the MCP server is configured to trust — never an arbitrary issuer from the
// token), dispatching on the trusted issuer's scheme:
//
//   - `file://<path>` → the Ed25519 desktop path: hard-requires alg EdDSA, loads
//     the public key from the path, verifies the signature.
//   - otherwise (an `https://` OIDC issuer) → verifies the signature against the
//     issuer's JWKS via oidc (must be non-nil for this branch).
//
// In both cases it then enforces `iss == trustedIssuer`, expiry against now, and
// the expected audience when expectedAudience is non-empty (the same per-env
// audience contract). It returns the validated claims or a descriptive error.
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

// isFileIssuer reports whether the trusted issuer is a `file://` desktop key
// issuer rather than an `https://` OIDC issuer.
func isFileIssuer(issuer string) bool {
	parsed, err := url.Parse(issuer)
	return err == nil && parsed.Scheme == "file"
}

// verifyFileMCPToken is the Ed25519 desktop-key path: alg is hard-locked
// to EdDSA, and the public key is loaded only from the trusted issuer's path.
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
	// The file:// token carries a single audience string; the per-env contract is
	// the same membership check the OIDC path uses, with a one-element list.
	if err := validateMCPAudience([]string{claims.Audience}, expectedAudience); err != nil {
		return MCPTokenClaims{}, err
	}
	return claims, nil
}

// verifyOIDCMCPToken is the OIDC issuer path: the signature is verified
// against the trusted issuer's JWKS by the shared verifier, then the same
// issuer / expiry / audience contract as the file:// path is enforced. The OIDC
// verifier already checks the signature and the standard time claims, so the
// returned claims' expiry is re-checked against `now` only for parity with the
// file:// path (a token go-oidc accepted is not expired at its own clock).
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

// mcpClaimsFromOIDC adapts the shared OIDCClaims into the MCP edge's claim view.
// The single-string Audience field is the file:// signed shape; for an OIDC
// token (which may carry several audiences) the audience contract is enforced
// separately on the full list, so this carries the first audience only as a
// human-readable hint and leaves the authoritative check to validateMCPAudience.
func mcpClaimsFromOIDC(verified OIDCClaims) MCPTokenClaims {
	claims := MCPTokenClaims{Issuer: verified.Issuer, Subject: verified.Subject}
	if exp, ok := verified.Raw["exp"].(float64); ok {
		claims.ExpiresAt = int64(exp)
	}
	if len(verified.Audience) > 0 {
		claims.Audience = verified.Audience[0]
	}
	return claims
}

// parseMCPToken splits a JWT into its signing input, decoded claims, and raw
// signature, hard-requiring the EdDSA algorithm in the header (closing the
// alg-confusion class — none / HMAC verified against the public key bytes).
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

// validateMCPExpiry rejects a token whose exp is at or before now. An absent exp
// (0) is not enforced, matching the original file:// behavior.
func validateMCPExpiry(expiresAt int64, now time.Time) error {
	if expiresAt != 0 && now.After(time.Unix(expiresAt, 0)) {
		return fmt.Errorf("MCP token expired")
	}
	return nil
}

// validateMCPAudience enforces the per-env audience contract: when
// expectedAudience is non-empty it must appear among the token's audiences. An
// empty expectedAudience disables the check (the chart did not pin an audience).
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

// UnverifiedMCPTokenIssuer extracts the `iss` claim from a token WITHOUT
// verifying its signature. The MCP server uses it — exactly as the erun api's
// IssuerFromUnverifiedJWT does — only to select which trusted issuer (and
// therefore which tenant) the token claims to come from; VerifyMCPToken then
// re-checks the issuer and verifies the signature against that issuer's key.
//
// Issuer selection is alg-agnostic so an RS256/ES256 OIDC token can be routed to
// its trusted entry: it reads `iss` from the payload without inspecting the
// JWS header's alg. The per-issuer alg policy (EdDSA-only for file:// issuers,
// JWKS-driven for OIDC issuers) is enforced later by VerifyMCPToken.
func UnverifiedMCPTokenIssuer(token string) (string, error) {
	return IssuerFromUnverifiedJWT(token)
}

// loadEd25519PublicKeyFromFileIssuer parses a `file://<path>` issuer and reads
// the PEM-encoded Ed25519 public key at that path.
func loadEd25519PublicKeyFromFileIssuer(issuer string) (ed25519.PublicKey, error) {
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Scheme != "file" {
		return nil, fmt.Errorf("trusted issuer %q is not a file:// URL", issuer)
	}
	// file://<path> — the path is parsed.Path (host is empty for file:///abs or
	// file://abs forms we mint via FileIssuer).
	path := parsed.Path
	if path == "" {
		path = parsed.Opaque
	}
	if parsed.Host != "" {
		path = parsed.Host + path
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read MCP trusted public key %s: %w", path, err)
	}
	return parseEd25519PublicKey(data)
}

// DesktopPublicKeyPEM derives the PKIX public-key PEM from a persisted desktop
// private key, so the private key (desktopid.key) is the single source of truth
// the desktop persists; the public half is recomputed for injection on deploy.
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
