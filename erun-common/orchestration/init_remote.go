package orchestration

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

type RemoteRepositorySpec struct {
	URL                string
	CodeCommitHost     string
	CodeCommitSSHKeyID string
	UseHostConfig      bool
}

var codeCommitHostPattern = regexp.MustCompile(`^git-codecommit\.[a-z0-9-]+\.amazonaws\.com(?:\.cn)?$`)

func ParseRemoteRepositorySpec(repositoryURL string) (RemoteRepositorySpec, error) {
	repositoryURL = strings.TrimSpace(repositoryURL)
	if repositoryURL == "" {
		return RemoteRepositorySpec{}, fmt.Errorf("git remote URL is required")
	}
	parseURL := repositoryURL
	codeCommitBareURL := strings.HasPrefix(parseURL, "git-codecommit.")
	if codeCommitBareURL {
		parseURL = "ssh://" + parseURL
	}
	if !codeCommitBareURL && !strings.Contains(parseURL, "://") {
		return RemoteRepositorySpec{URL: repositoryURL}, nil
	}
	parsed, err := url.Parse(parseURL)
	if err != nil {
		return RemoteRepositorySpec{}, err
	}
	host := strings.TrimSpace(parsed.Hostname())
	if !codeCommitHostPattern.MatchString(host) {
		return RemoteRepositorySpec{URL: repositoryURL}, nil
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "ssh"
	}
	keyID := ""
	if parsed.User != nil {
		keyID = parsed.User.Username()
		parsed.User = nil
	}
	return RemoteRepositorySpec{
		URL:                parsed.String(),
		CodeCommitHost:     host,
		CodeCommitSSHKeyID: keyID,
	}, nil
}

func CodeCommitSSHConfig(repository RemoteRepositorySpec, keyID string) string {
	keyID = strings.TrimSpace(keyID)
	if keyID == "" {
		keyID = "<SSH public key ID>"
	}
	return strings.Join([]string{
		"Host " + repository.CodeCommitHost,
		"  User " + keyID,
		"  IdentityFile ~/.ssh/id_rsa_codecommit",
		"  IdentitiesOnly yes",
		"  StrictHostKeyChecking accept-new",
	}, "\n")
}

func CodeCommitSetupDetails(repository RemoteRepositorySpec, publicKey, keyID string) string {
	return strings.Join([]string{
		"Upload this SSH public key to the IAM user that should access CodeCommit:",
		strings.TrimSpace(publicKey),
		"",
		"Use the SSH public key ID returned by IAM in this SSH host config:",
		CodeCommitSSHConfig(repository, keyID),
	}, "\n")
}
