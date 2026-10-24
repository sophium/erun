package doctor

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/remoteinit"
)

// RemoteInitFinishParams are the in-runtime doctor's recovery inputs.
// RepositoryURL and CodeCommitSSHKeyID fill gaps in the marker; nil Sleep uses 2s.
type RemoteInitFinishParams struct {
	HomeDir            string
	RepositoryURL      string
	CodeCommitSSHKeyID string
	Sleep              core.SleepFunc
}

// RemoteInitFinishPrompt asks for one value; label distinguishes the repository
// URL from the CodeCommit SSH key ID.
type RemoteInitFinishPrompt func(label string) (string, error)

// RunRemoteInitFinish runs the missing recovery steps (tracing them under
// dry-run), mirroring `erun init --remote`, and returns the updated inspection.
func RunRemoteInitFinish(ctx core.Context, inspection RemoteInitInspection, params RemoteInitFinishParams, prompt RemoteInitFinishPrompt) (RemoteInitInspection, error) {
	if inspection.HomeDir == "" {
		return inspection, errors.New("home directory is required to finish remote init")
	}
	if inspection.Tenant == "" || inspection.Environment == "" {
		return inspection, errors.New("remote init marker is missing tenant/environment and ERUN_TENANT/ERUN_ENVIRONMENT are unset")
	}
	if inspection.ProjectRoot == "" {
		return inspection, errors.New("remote init marker is missing project_root and ERUN_REPO_PATH is unset")
	}

	missing := inspection.MissingItems()
	if len(missing) == 0 {
		return inspection, nil
	}

	repositoryURL, err := resolveRemoteInitRepositoryURL(ctx, inspection, params, prompt, missing)
	if err != nil {
		return inspection, err
	}

	repository := resolveRemoteInitRepositorySpec(repositoryURL, inspection.Marker)
	missing = appendMissingCodeCommitKeypair(missing, repository, inspection.HomeDir)

	state, err := applyRemoteInitMissingItems(ctx, inspection, missing)
	if err != nil {
		return inspection, err
	}

	if state.needsGitCheckout {
		if err := runRemoteInitGitCheckout(ctx, inspection, &repository, state, params, prompt); err != nil {
			return inspection, err
		}
	}

	return saveRemoteInitCompletionMarker(ctx, inspection, repository, repositoryURL)
}

func resolveRemoteInitRepositoryURL(ctx core.Context, inspection RemoteInitInspection, params RemoteInitFinishParams, prompt RemoteInitFinishPrompt, missing []RemoteInitInspectionItem) (string, error) {
	repositoryURL := strings.TrimSpace(params.RepositoryURL)
	if repositoryURL == "" {
		repositoryURL = strings.TrimSpace(inspection.Marker.RepositoryURL)
	}
	needsClone := remoteInitMissingItem(missing, "git checkout")
	if needsClone && repositoryURL == "" && !ctx.DryRun {
		if prompt == nil {
			return "", errors.New("repository URL is required to finish git checkout")
		}
		value, err := prompt(remoteInitRepositoryPromptLabel(inspection.Tenant, inspection.Environment))
		if err != nil {
			return "", err
		}
		repositoryURL = strings.TrimSpace(value)
		if repositoryURL == "" {
			return "", errors.New("repository URL is required to finish git checkout")
		}
	}
	return repositoryURL, nil
}

func appendMissingCodeCommitKeypair(missing []RemoteInitInspectionItem, repository remoteinit.RemoteRepositorySpec, homeDir string) []RemoteInitInspectionItem {
	if repository.CodeCommitHost == "" || remoteInitMissingItem(missing, "CodeCommit SSH keypair") {
		return missing
	}
	rsaPath := defaultRemoteInitCodeCommitSSHKeyPath(homeDir)
	if _, err := os.Stat(rsaPath); err != nil {
		missing = append(missing, RemoteInitInspectionItem{
			Label:  "CodeCommit SSH keypair",
			Path:   rsaPath,
			Status: RemoteInitInspectionStatusMissing,
		})
	}
	return missing
}

type remoteInitKeyState struct {
	sshKeyPath                 string
	sshKeyJustGenerated        bool
	codeCommitKeyPath          string
	codeCommitKeyJustGenerated bool
	needsGitCheckout           bool
}

func applyRemoteInitMissingItems(ctx core.Context, inspection RemoteInitInspection, missing []RemoteInitInspectionItem) (remoteInitKeyState, error) {
	state := remoteInitKeyState{sshKeyPath: defaultRemoteInitSSHKeyPath(inspection.HomeDir)}
	for _, item := range orderRemoteInitMissingItems(missing) {
		if err := applyRemoteInitMissingItem(ctx, inspection, item, &state); err != nil {
			return state, err
		}
	}
	return state, nil
}

// applyRemoteInitMissingItem materializes one item; the clone is deferred
// until every key exists.
func applyRemoteInitMissingItem(ctx core.Context, inspection RemoteInitInspection, item RemoteInitInspectionItem, state *remoteInitKeyState) error {
	switch item.Label {
	case "project root":
		return finishRemoteInitProjectRoot(ctx, item.Path)
	case "SSH keypair":
		if err := finishRemoteInitSSHKey(ctx, item.Path); err != nil {
			return err
		}
		state.sshKeyPath = item.Path
		state.sshKeyJustGenerated = true
	case "CodeCommit SSH keypair":
		if err := finishRemoteInitCodeCommitSSHKey(ctx, item.Path); err != nil {
			return err
		}
		state.codeCommitKeyPath = item.Path
		state.codeCommitKeyJustGenerated = true
	case "git checkout":
		state.needsGitCheckout = true
	case "agent skills":
		return finishRemoteInitSkills(ctx, inspection.HomeDir)
	}
	return nil
}

// runRemoteInitGitCheckout may set repository's CodeCommit SSH key ID.
func runRemoteInitGitCheckout(ctx core.Context, inspection RemoteInitInspection, repository *remoteinit.RemoteRepositorySpec, state remoteInitKeyState, params RemoteInitFinishParams, prompt RemoteInitFinishPrompt) error {
	codeCommitKeyPath := state.codeCommitKeyPath
	if repository.CodeCommitHost != "" {
		if codeCommitKeyPath == "" {
			codeCommitKeyPath = defaultRemoteInitCodeCommitSSHKeyPath(inspection.HomeDir)
		}
		if err := resolveCodeCommitSSHKeyID(ctx, repository, codeCommitKeyPath, inspection.Tenant, inspection.Environment, params.CodeCommitSSHKeyID, prompt); err != nil {
			return err
		}
		if err := finishRemoteInitCodeCommitSSHConfig(ctx, inspection.HomeDir, *repository); err != nil {
			return err
		}
	}
	if err := finishRemoteInitGitAccess(ctx, state.sshKeyPath, codeCommitKeyPath, *repository, state.sshKeyJustGenerated, state.codeCommitKeyJustGenerated, params.Sleep); err != nil {
		return err
	}
	return finishRemoteInitGitCheckout(ctx, inspection.ProjectRoot, state.sshKeyPath, *repository)
}

func saveRemoteInitCompletionMarker(ctx core.Context, inspection RemoteInitInspection, repository remoteinit.RemoteRepositorySpec, repositoryURL string) (RemoteInitInspection, error) {
	updated := inspection
	updated.Marker.Tenant = inspection.Tenant
	updated.Marker.Environment = inspection.Environment
	updated.Marker.ProjectRoot = inspection.ProjectRoot
	if !updated.Marker.NoGit {
		updated.Marker.RepositoryURL = remoteInitMarkerRepositoryURL(repository, repositoryURL)
		updated.Marker.CodeCommitHost = repository.CodeCommitHost
		updated.Marker.CodeCommitSSHKeyID = repository.CodeCommitSSHKeyID
	}
	updated.Marker.BootstrapComplete = true
	updated.MarkerPresent = true

	if !ctx.DryRun {
		if err := remoteinit.SaveRemoteInitMarker(inspection.HomeDir, updated.Marker); err != nil {
			return inspection, err
		}
	} else {
		ctx.TraceCommand("", "write-yaml", remoteinit.RemoteInitMarkerPath(inspection.HomeDir, updated.Marker.Tenant, updated.Marker.Environment))
	}
	return updated, nil
}

// resolveCodeCommitSSHKeyID prints the public key before prompting, because IAM
// issues the key ID only after the key is uploaded.
func resolveCodeCommitSSHKeyID(ctx core.Context, repository *remoteinit.RemoteRepositorySpec, codeCommitKeyPath, tenant, environment, paramKeyID string, prompt RemoteInitFinishPrompt) error {
	if repository.CodeCommitSSHKeyID != "" || ctx.DryRun {
		return nil
	}
	keyID := strings.TrimSpace(paramKeyID)
	if keyID == "" {
		publicKey, err := readRemoteInitPublicKey(codeCommitKeyPath)
		if err != nil {
			return err
		}
		ctx.Info(remoteinit.CodeCommitSetupDetails(*repository, publicKey, "<SSH public key ID>"))
		if prompt == nil {
			return errors.New("CodeCommit SSH public key ID is required to finish git checkout")
		}
		value, err := prompt(remoteinit.CodeCommitSSHKeyIDLabel(tenant, environment))
		if err != nil {
			return err
		}
		keyID = strings.TrimSpace(value)
	}
	if keyID == "" {
		return errors.New("CodeCommit SSH public key ID is required to finish git checkout")
	}
	repository.CodeCommitSSHKeyID = keyID
	return nil
}

// resolveRemoteInitRepositorySpec shares init's URL parser; a blank URL yields
// a zero spec, treated as plain SSH.
func resolveRemoteInitRepositorySpec(repositoryURL string, marker remoteinit.RemoteInitMarker) remoteinit.RemoteRepositorySpec {
	repositoryURL = strings.TrimSpace(repositoryURL)
	if repositoryURL == "" {
		return remoteinit.RemoteRepositorySpec{
			CodeCommitHost:     strings.TrimSpace(marker.CodeCommitHost),
			CodeCommitSSHKeyID: strings.TrimSpace(marker.CodeCommitSSHKeyID),
		}
	}
	parsed, err := remoteinit.ParseRemoteRepositorySpec(repositoryURL)
	if err != nil {
		return remoteinit.RemoteRepositorySpec{URL: repositoryURL}
	}
	if parsed.CodeCommitHost != "" && parsed.CodeCommitSSHKeyID == "" {
		parsed.CodeCommitSSHKeyID = strings.TrimSpace(marker.CodeCommitSSHKeyID)
	}
	return parsed
}

func remoteInitMarkerRepositoryURL(repository remoteinit.RemoteRepositorySpec, fallback string) string {
	if strings.TrimSpace(repository.URL) != "" {
		return repository.URL
	}
	return strings.TrimSpace(fallback)
}

// orderRemoteInitMissingItems puts the project root and keypairs before the
// checkout, including a dynamically appended CodeCommit item.
func orderRemoteInitMissingItems(items []RemoteInitInspectionItem) []RemoteInitInspectionItem {
	order := []string{"project root", "SSH keypair", "CodeCommit SSH keypair", "git checkout", "agent skills"}
	ordered := make([]RemoteInitInspectionItem, 0, len(items))
	for _, label := range order {
		for _, item := range items {
			if item.Label == label {
				ordered = append(ordered, item)
			}
		}
	}
	return ordered
}

func remoteInitMissingItem(items []RemoteInitInspectionItem, label string) bool {
	for _, item := range items {
		if item.Label == label {
			return true
		}
	}
	return false
}

// WriteRemoteInitInspectionReport renders the inspection to ctx.Stdout.
func WriteRemoteInitInspectionReport(ctx core.Context, inspection RemoteInitInspection) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Remote init checks for %s/%s:\n", remoteInitOrUnknown(inspection.Tenant), remoteInitOrUnknown(inspection.Environment)); err != nil {
		return err
	}
	markerPath := remoteinit.RemoteInitMarkerPath(inspection.HomeDir, inspection.Tenant, inspection.Environment)
	if !inspection.MarkerPresent {
		if _, err := fmt.Fprintf(ctx.Stdout, "  marker file                 MISSING %s\n", quotedPathForReport(markerPath)); err != nil {
			return err
		}
	} else {
		state := "incomplete"
		if inspection.Marker.BootstrapComplete {
			state = "complete"
		}
		if _, err := fmt.Fprintf(ctx.Stdout, "  marker file                 %s %s\n", strings.ToUpper(state), quotedPathForReport(markerPath)); err != nil {
			return err
		}
	}
	for _, item := range inspection.Items {
		label := fmt.Sprintf("  %-26s", item.Label)
		path := item.Path
		if path == "" {
			path = "<unknown>"
		}
		if _, err := fmt.Fprintf(ctx.Stdout, "%s %s %s\n", label, strings.ToUpper(string(item.Status)), quotedPathForReport(path)); err != nil {
			return err
		}
	}
	return nil
}

func quotedPathForReport(value string) string {
	if value == "" {
		return "'<unknown>'"
	}
	return "'" + value + "'"
}

func remoteInitOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "<unknown>"
	}
	return value
}

func remoteInitRepositoryPromptLabel(tenant, environment string) string {
	return fmt.Sprintf("Git remote URL for environment %q in tenant %q", environment, tenant)
}
