package eruncommon

import (
	"bufio"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var awsConfigUserHomeDir = os.UserHomeDir

// AWSSSOProfile is the subset of an AWS shared config profile the doctor needs
// to pre-fill an InitAWSCloudProviderParams; fields are trimmed and empty only
// when the source omitted the entry.
type AWSSSOProfile struct {
	Profile     string
	SSOStartURL string
	SSORegion   string
	AccountID   string
	RoleName    string
	Region      string
}

// LookupAWSSSOProfileByAccountID returns the AWS shared-config profile matching
// the given account id, resolving both inline SSO settings and the sso_session
// indirection. ok is false when no profile matches or the config file is absent.
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
	kind   string
	name   string
	values map[string]string
}

// parseAWSSharedConfig deliberately parses only flat top-level keys; the nested
// "s3 ="-style blocks some configs use carry no SSO settings, so it skips them.
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
