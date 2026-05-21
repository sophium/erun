package eruncommon

import (
	"os"
	"path/filepath"
	"testing"
)

func writeAWSConfig(t *testing.T, body string) func() {
	t.Helper()
	tmp := t.TempDir()
	awsDir := filepath.Join(tmp, ".aws")
	if err := os.MkdirAll(awsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(awsDir, "config"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	previous := awsConfigUserHomeDir
	awsConfigUserHomeDir = func() (string, error) { return tmp, nil }
	prevEnv, hadEnv := os.LookupEnv("AWS_CONFIG_FILE")
	_ = os.Unsetenv("AWS_CONFIG_FILE")
	return func() {
		awsConfigUserHomeDir = previous
		if hadEnv {
			_ = os.Setenv("AWS_CONFIG_FILE", prevEnv)
		} else {
			_ = os.Unsetenv("AWS_CONFIG_FILE")
		}
	}
}

// TestLookupAWSSSOProfileByAccountIDInlineProfile covers the legacy
// inline form: a profile that carries sso_start_url, sso_region,
// sso_account_id directly. This is the shape erun's own
// runCloudInitAWSCommand writes today, so the doctor's discovery
// must round-trip its own output cleanly.
func TestLookupAWSSSOProfileByAccountIDInlineProfile(t *testing.T) {
	restore := writeAWSConfig(t, `[default]
[profile erun-sso-20260427124244]
sso_start_url = https://d-9c674ca9d9.awsapps.com/start
sso_region = eu-west-2
sso_account_id = 020362606330
sso_role_name = ErunDeveloper
region = eu-west-2
`)
	defer restore()

	got, ok, err := LookupAWSSSOProfileByAccountID("020362606330")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !ok {
		t.Fatal("expected match")
	}
	if got.SSOStartURL != "https://d-9c674ca9d9.awsapps.com/start" {
		t.Fatalf("start URL: %q", got.SSOStartURL)
	}
	if got.SSORegion != "eu-west-2" {
		t.Fatalf("sso region: %q", got.SSORegion)
	}
	if got.Region != "eu-west-2" {
		t.Fatalf("region: %q", got.Region)
	}
	if got.RoleName != "ErunDeveloper" {
		t.Fatalf("role name: %q", got.RoleName)
	}
	if got.Profile != "erun-sso-20260427124244" {
		t.Fatalf("profile: %q", got.Profile)
	}
}

// TestLookupAWSSSOProfileByAccountIDSSOSessionIndirection covers the
// modern shape where a profile references an [sso-session] block via
// sso_session = name. The lookup must resolve the indirection so the
// SSO start URL is still discoverable.
func TestLookupAWSSSOProfileByAccountIDSSOSessionIndirection(t *testing.T) {
	restore := writeAWSConfig(t, `[sso-session my-org]
sso_start_url = https://org.awsapps.com/start
sso_region = eu-west-1
sso_registration_scopes = sso:account:access

[profile worker]
sso_session = my-org
sso_account_id = 999999999999
sso_role_name = Developer
region = eu-west-1
`)
	defer restore()

	got, ok, err := LookupAWSSSOProfileByAccountID("999999999999")
	if err != nil || !ok {
		t.Fatalf("lookup: ok=%v err=%v", ok, err)
	}
	if got.SSOStartURL != "https://org.awsapps.com/start" {
		t.Fatalf("start URL: %q", got.SSOStartURL)
	}
	if got.SSORegion != "eu-west-1" {
		t.Fatalf("sso region: %q", got.SSORegion)
	}
}

// TestLookupAWSSSOProfileByAccountIDInlinePrecedence guarantees that
// when a profile carries both inline sso_start_url and a sso_session
// reference, the inline value wins. Inline is the explicit, more
// specific source; treating it otherwise would surprise users who
// override one field in a profile they inherited.
func TestLookupAWSSSOProfileByAccountIDInlinePrecedence(t *testing.T) {
	restore := writeAWSConfig(t, `[sso-session my-org]
sso_start_url = https://org.awsapps.com/start
sso_region = eu-west-1

[profile worker]
sso_session = my-org
sso_start_url = https://override.awsapps.com/start
sso_region = us-east-1
sso_account_id = 123456789012
`)
	defer restore()

	got, ok, err := LookupAWSSSOProfileByAccountID("123456789012")
	if err != nil || !ok {
		t.Fatalf("lookup: %v / %v", ok, err)
	}
	if got.SSOStartURL != "https://override.awsapps.com/start" {
		t.Fatalf("inline must win, got %q", got.SSOStartURL)
	}
	if got.SSORegion != "us-east-1" {
		t.Fatalf("inline region must win, got %q", got.SSORegion)
	}
}

// TestLookupAWSSSOProfileByAccountIDMissingReturnsNotFound: no
// matching profile must produce ok=false, err=nil. Doctor relies on
// that contract to fall back to manual prompts without surfacing a
// fake error.
func TestLookupAWSSSOProfileByAccountIDMissingReturnsNotFound(t *testing.T) {
	restore := writeAWSConfig(t, `[default]
region = us-east-1
`)
	defer restore()
	got, ok, err := LookupAWSSSOProfileByAccountID("000000000000")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatalf("expected no match, got %+v", got)
	}
}

// TestLookupAWSSSOProfileByAccountIDMissingFileReturnsNotFound: the
// shared config file may not exist at all (fresh machine). That is
// not an error condition for the doctor — it just means we cannot
// pre-fill anything and must prompt manually.
func TestLookupAWSSSOProfileByAccountIDMissingFileReturnsNotFound(t *testing.T) {
	previous := awsConfigUserHomeDir
	awsConfigUserHomeDir = func() (string, error) { return t.TempDir(), nil }
	prevEnv, hadEnv := os.LookupEnv("AWS_CONFIG_FILE")
	_ = os.Unsetenv("AWS_CONFIG_FILE")
	defer func() {
		awsConfigUserHomeDir = previous
		if hadEnv {
			_ = os.Setenv("AWS_CONFIG_FILE", prevEnv)
		} else {
			_ = os.Unsetenv("AWS_CONFIG_FILE")
		}
	}()
	_, ok, err := LookupAWSSSOProfileByAccountID("020362606330")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if ok {
		t.Fatal("expected no match when ~/.aws/config does not exist")
	}
}

// TestLookupAWSSSOProfileHonorsAWSConfigFileEnv: the AWS CLI lets
// users redirect the shared config file via AWS_CONFIG_FILE. erun
// honors the same variable so users who keep multiple work configs
// (and switch between them with envrc shims) see the discovery
// against the active file.
func TestLookupAWSSSOProfileHonorsAWSConfigFileEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.alt")
	if err := os.WriteFile(path, []byte(`[profile alt]
sso_start_url = https://alt.awsapps.com/start
sso_region = us-west-2
sso_account_id = 111111111111
`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	prevEnv, hadEnv := os.LookupEnv("AWS_CONFIG_FILE")
	_ = os.Setenv("AWS_CONFIG_FILE", path)
	defer func() {
		if hadEnv {
			_ = os.Setenv("AWS_CONFIG_FILE", prevEnv)
		} else {
			_ = os.Unsetenv("AWS_CONFIG_FILE")
		}
	}()
	got, ok, err := LookupAWSSSOProfileByAccountID("111111111111")
	if err != nil || !ok {
		t.Fatalf("lookup: %v / %v", ok, err)
	}
	if got.SSOStartURL != "https://alt.awsapps.com/start" {
		t.Fatalf("start URL: %q", got.SSOStartURL)
	}
}

// TestParseAWSSharedConfigSkipsCommentsAndBlankLines documents the
// parser's whitespace tolerance: comment characters anchored at the
// start of a line are stripped, blank lines are ignored, and value
// trimming is applied to both keys and values.
func TestParseAWSSharedConfigSkipsCommentsAndBlankLines(t *testing.T) {
	restore := writeAWSConfig(t, `
# leading comment
[profile good]
   sso_account_id = 222222222222
   sso_start_url = https://x.awsapps.com/start
; semicolon comment
   sso_region    = eu-west-3
`)
	defer restore()
	got, ok, err := LookupAWSSSOProfileByAccountID("222222222222")
	if err != nil || !ok {
		t.Fatalf("lookup: %v / %v", ok, err)
	}
	if got.SSORegion != "eu-west-3" || got.SSOStartURL != "https://x.awsapps.com/start" {
		t.Fatalf("trimmed values not parsed: %+v", got)
	}
}
