package eruncommon

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// awsConfigUserHomeDir is the seam tests use to point lookups at a
// fake home directory. Mirrors the openUserHomeDir / hostUserHomeDir
// pattern used elsewhere in this package so we do not invent a new
// override mechanism just for this file.
var awsConfigUserHomeDir = os.UserHomeDir

// AWSSSOProfile is the subset of an AWS shared config profile the
// doctor needs to pre-fill an InitAWSCloudProviderParams. Fields are
// trimmed and never empty unless the source file omitted the entry.
type AWSSSOProfile struct {
	Profile     string
	SSOStartURL string
	SSORegion   string
	AccountID   string
	RoleName    string
	Region      string
}

// LookupAWSSSOProfileByAccountID scans ~/.aws/config (or whichever
// path AWS_CONFIG_FILE overrides it to) and returns the first profile
// whose sso_account_id matches the supplied account. Returns
// ok=false when no profile matches or the file does not exist.
//
// Both legacy "inline" SSO settings (the profile carries
// sso_start_url, sso_region, sso_account_id directly) and the newer
// sso_session indirection ("sso_session = foo" → look up the
// matching [sso-session foo] block for sso_start_url + sso_region)
// are resolved. Profiles whose sso_account_id is empty are
// considered non-matches.
func LookupAWSSSOProfileByAccountID(accountID string) (AWSSSOProfile, bool, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return AWSSSOProfile{}, false, errors.New("account id is required")
	}
	path, err := awsConfigPath()
	if err != nil {
		return AWSSSOProfile{}, false, err
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return AWSSSOProfile{}, false, nil
		}
		return AWSSSOProfile{}, false, err
	}
	defer func() { _ = file.Close() }()
	sections, err := parseAWSSharedConfig(file)
	if err != nil {
		return AWSSSOProfile{}, false, err
	}
	profile, ok := findAWSSSOProfileForAccount(sections, accountID)
	return profile, ok, nil
}

func findAWSSSOProfileForAccount(sections []awsConfigSection, accountID string) (AWSSSOProfile, bool) {
	sessions := buildAWSSSOSessionIndex(sections)
	for _, section := range sections {
		if section.kind != "profile" {
			continue
		}
		if strings.TrimSpace(section.values["sso_account_id"]) != accountID {
			continue
		}
		profile := AWSSSOProfile{
			Profile:     section.name,
			SSOStartURL: strings.TrimSpace(section.values["sso_start_url"]),
			SSORegion:   strings.TrimSpace(section.values["sso_region"]),
			AccountID:   accountID,
			RoleName:    strings.TrimSpace(section.values["sso_role_name"]),
			Region:      strings.TrimSpace(section.values["region"]),
		}
		if sessionName := strings.TrimSpace(section.values["sso_session"]); sessionName != "" {
			if session, ok := sessions[sessionName]; ok {
				if profile.SSOStartURL == "" {
					profile.SSOStartURL = strings.TrimSpace(session.values["sso_start_url"])
				}
				if profile.SSORegion == "" {
					profile.SSORegion = strings.TrimSpace(session.values["sso_region"])
				}
			}
		}
		return profile, true
	}
	return AWSSSOProfile{}, false
}

func buildAWSSSOSessionIndex(sections []awsConfigSection) map[string]awsConfigSection {
	out := make(map[string]awsConfigSection)
	for _, section := range sections {
		if section.kind != "sso-session" {
			continue
		}
		out[section.name] = section
	}
	return out
}

type awsConfigSection struct {
	// kind is "profile" or "sso-session"; the "default" block is
	// surfaced as kind="profile", name="default".
	kind   string
	name   string
	values map[string]string
}

// parseAWSSharedConfig parses the INI-flavored AWS shared config.
// Only the subset we actually need is supported: top-level keys
// inside a section. Nested-key blocks (the "s3 =" multi-line form
// used by some advanced configs) are skipped — they have no overlap
// with SSO settings.
func parseAWSSharedConfig(r io.Reader) ([]awsConfigSection, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	sections := make([]awsConfigSection, 0, 8)
	var current *awsConfigSection
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sections = appendIfNotNil(sections, current)
			next := newAWSConfigSection(line)
			current = &next
			continue
		}
		if current == nil {
			continue
		}
		// Nested-key continuation lines start with whitespace in the
		// original file, which TrimSpace strips. Anything without "="
		// at this point is a nested-block value we can safely ignore.
		eq := strings.Index(line, "=")
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(line[eq+1:])
		current.values[key] = value
	}
	sections = appendIfNotNil(sections, current)
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return sections, nil
}

func appendIfNotNil(sections []awsConfigSection, current *awsConfigSection) []awsConfigSection {
	if current == nil {
		return sections
	}
	return append(sections, *current)
}

func newAWSConfigSection(header string) awsConfigSection {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(header, "["), "]"))
	switch {
	case inner == "default":
		return awsConfigSection{kind: "profile", name: "default", values: make(map[string]string)}
	case strings.HasPrefix(inner, "profile "):
		return awsConfigSection{kind: "profile", name: strings.TrimSpace(strings.TrimPrefix(inner, "profile ")), values: make(map[string]string)}
	case strings.HasPrefix(inner, "sso-session "):
		return awsConfigSection{kind: "sso-session", name: strings.TrimSpace(strings.TrimPrefix(inner, "sso-session ")), values: make(map[string]string)}
	default:
		return awsConfigSection{kind: "other", name: inner, values: make(map[string]string)}
	}
}

// awsConfigPath returns the path the AWS CLI would read for its
// shared config. AWS_CONFIG_FILE wins when set; otherwise the
// canonical "~/.aws/config" applies.
func awsConfigPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("AWS_CONFIG_FILE")); override != "" {
		return override, nil
	}
	home, err := awsConfigUserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".aws", "config"), nil
}
