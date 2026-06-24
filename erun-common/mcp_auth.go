package eruncommon

import (
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

// MCP auth edge (issue #655). The per-env erun-mcp server is exposed publicly
// and its `raw` tool can kubectl-exec, so it must always be authenticated. For
// a desktop deployment the trust anchor is a self-contained Ed25519 keypair:
// the desktop signs an EdDSA JWT bearer token with its private key
// (desktopid.key), injects the matching public key into the runtime pod, and
// names that public key in the token's `iss` claim as a `file://<path>` URL.
// The MCP server is configured to trust exactly that issuer, loads the public
// key from the path, and verifies the token's signature against it.
//
// The format is deliberately minimal and fully controlled (this package both
// signs and verifies): EdDSA only — `alg` is hard-checked, so the JWT
// alg-confusion class (accepting `none`/HMAC/RS256 against a public key) cannot
// occur — and the verifier only ever loads the public key from the issuer the
// caller already trusts, never an arbitrary `file://` from the token.

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
// the MCP server verifies bearer tokens against the public key (issue #655).
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

// VerifyMCPToken verifies an MCP bearer token. It hard-requires alg EdDSA,
// requires the token's issuer to equal trustedIssuer (a `file://<path>` URL the
// MCP server is configured to trust — never an arbitrary issuer from the
// token), loads the Ed25519 public key from that path, verifies the signature,
// checks expiry against now, and checks the audience when expectedAudience is
// non-empty. It returns the validated claims or a descriptive error.
func VerifyMCPToken(token, trustedIssuer, expectedAudience string, now time.Time) (MCPTokenClaims, error) {
	trustedIssuer = strings.TrimSpace(trustedIssuer)
	if trustedIssuer == "" {
		return MCPTokenClaims{}, fmt.Errorf("no trusted issuer configured for MCP auth")
	}
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
	if err := validateMCPClaims(claims, expectedAudience, now); err != nil {
		return MCPTokenClaims{}, err
	}
	return claims, nil
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

// validateMCPClaims checks the time and audience constraints on already
// signature-verified claims.
func validateMCPClaims(claims MCPTokenClaims, expectedAudience string, now time.Time) error {
	if claims.ExpiresAt != 0 && now.After(time.Unix(claims.ExpiresAt, 0)) {
		return fmt.Errorf("MCP token expired")
	}
	if expectedAudience != "" && claims.Audience != expectedAudience {
		return fmt.Errorf("MCP token audience %q does not match %q", claims.Audience, expectedAudience)
	}
	return nil
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
