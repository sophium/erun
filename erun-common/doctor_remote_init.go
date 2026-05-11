package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RemoteInitInspectionStatus categorizes one expected artifact's state.
type RemoteInitInspectionStatus string

const (
	RemoteInitInspectionStatusOK            RemoteInitInspectionStatus = "ok"
	RemoteInitInspectionStatusMissing       RemoteInitInspectionStatus = "missing"
	RemoteInitInspectionStatusNotApplicable RemoteInitInspectionStatus = "n/a"
)

// RemoteInitInspectionItem is one row in the inspection report.
type RemoteInitInspectionItem struct {
	Label  string
	Path   string
	Status RemoteInitInspectionStatus
}

// RemoteInitInspection is the doctor's view of remote-init state from
// inside the runtime pod: what the marker recorded and which local
// filesystem artifacts match that intent.
type RemoteInitInspection struct {
	MarkerPresent bool
	Marker        RemoteInitMarker

	// HomeDir, Tenant, Environment, ProjectRoot are the resolved values
	// doctor is operating against. When the marker is missing the fields
	// fall back to the runtime-pod env vars (ERUN_TENANT etc.) so the
	// report can still describe the target.
	HomeDir     string
	Tenant      string
	Environment string
	ProjectRoot string

	Items []RemoteInitInspectionItem
}

// Complete reports whether every applicable artifact matches the
// marker's intent. It is the signal doctor uses to decide whether to
// offer recovery.
func (r RemoteInitInspection) Complete() bool {
	if !r.MarkerPresent {
		return false
	}
	if !r.Marker.BootstrapComplete {
		return false
	}
	for _, item := range r.Items {
		if item.Status == RemoteInitInspectionStatusMissing {
			return false
		}
	}
	return true
}

// MissingItems returns only the items doctor would offer to finish.
func (r RemoteInitInspection) MissingItems() []RemoteInitInspectionItem {
	missing := make([]RemoteInitInspectionItem, 0, len(r.Items))
	for _, item := range r.Items {
		if item.Status == RemoteInitInspectionStatusMissing {
			missing = append(missing, item)
		}
	}
	return missing
}

// IsInRuntimeEnvironment reports whether the current process is running
// inside an erun runtime pod, based on env vars the deployment template
// sets on every runtime container.
func IsInRuntimeEnvironment(env func(string) string) bool {
	if env == nil {
		env = os.Getenv
	}
	return strings.EqualFold(strings.TrimSpace(env("ERUN_REPO_REMOTE")), "true")
}

// InspectRemoteInit reads the marker (if any) and probes the local
// filesystem for the artifacts init was expected to produce. The
// returned inspection is purely descriptive — no side effects.
func InspectRemoteInit(homeDir string, env func(string) string) (RemoteInitInspection, error) {
	if env == nil {
		env = os.Getenv
	}
	marker, present, err := LoadRemoteInitMarker(homeDir)
	if err != nil {
		return RemoteInitInspection{}, err
	}
	inspection := RemoteInitInspection{
		MarkerPresent: present,
		Marker:        marker,
		HomeDir:       homeDir,
	}
	if present {
		inspection.Tenant = marker.Tenant
		inspection.Environment = marker.Environment
		inspection.ProjectRoot = marker.ProjectRoot
	}
	if inspection.Tenant == "" {
		inspection.Tenant = strings.TrimSpace(env("ERUN_TENANT"))
	}
	if inspection.Environment == "" {
		inspection.Environment = strings.TrimSpace(env("ERUN_ENVIRONMENT"))
	}
	if inspection.ProjectRoot == "" {
		inspection.ProjectRoot = strings.TrimSpace(env("ERUN_REPO_PATH"))
	}

	inspection.Items = append(inspection.Items, inspectProjectRoot(inspection.ProjectRoot))
	inspection.Items = append(inspection.Items, inspectGitCheckout(inspection.ProjectRoot, marker, present))
	inspection.Items = append(inspection.Items, inspectSSHKey(homeDir, marker, present))
	return inspection, nil
}

func inspectProjectRoot(projectRoot string) RemoteInitInspectionItem {
	item := RemoteInitInspectionItem{
		Label: "project root",
		Path:  projectRoot,
	}
	if projectRoot == "" {
		item.Status = RemoteInitInspectionStatusMissing
		return item
	}
	info, err := os.Stat(projectRoot)
	if err != nil || !info.IsDir() {
		item.Status = RemoteInitInspectionStatusMissing
		return item
	}
	item.Status = RemoteInitInspectionStatusOK
	return item
}

func inspectGitCheckout(projectRoot string, marker RemoteInitMarker, markerPresent bool) RemoteInitInspectionItem {
	gitDir := ""
	if projectRoot != "" {
		gitDir = filepath.Join(projectRoot, ".git")
	}
	item := RemoteInitInspectionItem{
		Label: "git checkout",
		Path:  gitDir,
	}
	if markerPresent && marker.NoGit {
		item.Status = RemoteInitInspectionStatusNotApplicable
		return item
	}
	if projectRoot == "" {
		item.Status = RemoteInitInspectionStatusMissing
		return item
	}
	if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
		item.Status = RemoteInitInspectionStatusOK
		return item
	}
	item.Status = RemoteInitInspectionStatusMissing
	return item
}

func inspectSSHKey(homeDir string, marker RemoteInitMarker, markerPresent bool) RemoteInitInspectionItem {
	keyPath := filepath.Join(homeDir, ".ssh", "id_ed25519")
	item := RemoteInitInspectionItem{
		Label: "SSH keypair",
		Path:  keyPath,
	}
	if markerPresent && marker.NoGit {
		item.Status = RemoteInitInspectionStatusNotApplicable
		return item
	}
	if _, err := os.Stat(keyPath); err == nil {
		item.Status = RemoteInitInspectionStatusOK
		return item
	}
	item.Status = RemoteInitInspectionStatusMissing
	return item
}

// RemoteInitFinishParams carries the inputs the in-runtime doctor needs
// to drive a recovery in non-dry-run mode. RepositoryURL is required
// when the marker did not record one (e.g., init was interrupted before
// the URL was resolved) and the user has not yet been prompted.
type RemoteInitFinishParams struct {
	HomeDir       string
	RepositoryURL string
}

// RemoteInitFinishPrompt requests one value from the caller. The CLI
// implements this with a promptui prompt; tests pass a stub.
type RemoteInitFinishPrompt func(label string) (string, error)

// RemoteInitFinishConfirm asks the caller a yes/no question. doctor
// uses it to pause after generating an SSH key so the user can import
// the public key on their git host before doctor attempts the clone.
type RemoteInitFinishConfirm func(label string) (bool, error)

// RunRemoteInitFinish executes whichever recovery steps the inspection
// flagged as missing. In dry-run mode it traces the steps it would run
// without performing them. Returns the updated inspection so callers
// can render a final report.
func RunRemoteInitFinish(ctx Context, inspection RemoteInitInspection, params RemoteInitFinishParams, prompt RemoteInitFinishPrompt, confirm RemoteInitFinishConfirm) (RemoteInitInspection, error) {
	if inspection.HomeDir == "" {
		return inspection, errors.New("home directory is required to finish remote init")
	}
	if inspection.Tenant == "" || inspection.Environment == "" {
		return inspection, errors.New("remote init marker is missing tenant/environment and ERUN_TENANT/ERUN_ENVIRONMENT are unset")
	}
	if inspection.ProjectRoot == "" {
		return inspection, errors.New("remote init marker is missing project_root and ERUN_REPO_PATH is unset")
	}

	missing := orderedMissingItems(inspection)
	if len(missing) == 0 {
		return inspection, nil
	}

	repositoryURL := strings.TrimSpace(params.RepositoryURL)
	if repositoryURL == "" {
		repositoryURL = strings.TrimSpace(inspection.Marker.RepositoryURL)
	}
	needsClone := remoteInitMissingItem(missing, "git checkout")
	if needsClone && repositoryURL == "" && !ctx.DryRun {
		if prompt == nil {
			return inspection, errors.New("repository URL is required to finish git checkout")
		}
		value, err := prompt(remoteInitRepositoryPromptLabel(inspection.Tenant, inspection.Environment))
		if err != nil {
			return inspection, err
		}
		repositoryURL = strings.TrimSpace(value)
		if repositoryURL == "" {
			return inspection, errors.New("repository URL is required to finish git checkout")
		}
	}

	sshKeyPath := ""
	sshKeyJustGenerated := false
	for _, item := range missing {
		switch item.Label {
		case "project root":
			if err := finishRemoteInitProjectRoot(ctx, item.Path); err != nil {
				return inspection, err
			}
		case "SSH keypair":
			if err := finishRemoteInitSSHKey(ctx, item.Path); err != nil {
				return inspection, err
			}
			sshKeyPath = item.Path
			sshKeyJustGenerated = true
		case "git checkout":
			if sshKeyPath == "" {
				sshKeyPath = defaultRemoteInitSSHKeyPath(inspection.HomeDir)
			}
			if err := finishRemoteInitGitAccess(ctx, sshKeyPath, repositoryURL, sshKeyJustGenerated, confirm); err != nil {
				return inspection, err
			}
			if err := finishRemoteInitGitCheckout(ctx, inspection.ProjectRoot, sshKeyPath, repositoryURL); err != nil {
				return inspection, err
			}
		}
	}

	updated := inspection
	updated.Marker.Tenant = inspection.Tenant
	updated.Marker.Environment = inspection.Environment
	updated.Marker.ProjectRoot = inspection.ProjectRoot
	if !updated.Marker.NoGit {
		updated.Marker.RepositoryURL = repositoryURL
	}
	updated.Marker.BootstrapComplete = true
	updated.MarkerPresent = true

	if !ctx.DryRun {
		if err := SaveRemoteInitMarker(inspection.HomeDir, updated.Marker); err != nil {
			return inspection, err
		}
	} else {
		ctx.TraceCommand("", "write-yaml", RemoteInitMarkerPath(inspection.HomeDir))
	}
	return updated, nil
}

// orderedMissingItems returns the missing artifacts in the order doctor
// must finish them: the project root and SSH keypair are prerequisites
// for the git checkout, so they go first regardless of how the
// inspection records them.
func orderedMissingItems(inspection RemoteInitInspection) []RemoteInitInspectionItem {
	order := []string{"project root", "SSH keypair", "git checkout"}
	missing := inspection.MissingItems()
	ordered := make([]RemoteInitInspectionItem, 0, len(missing))
	for _, label := range order {
		for _, item := range missing {
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

func finishRemoteInitProjectRoot(ctx Context, path string) error {
	ctx.TraceCommand("", "mkdir", "-p", path)
	if ctx.DryRun {
		return nil
	}
	return os.MkdirAll(path, 0o755)
}

func finishRemoteInitSSHKey(ctx Context, path string) error {
	ctx.TraceCommand("", "ssh-keygen", "-t", "ed25519", "-N", "", "-f", path)
	if ctx.DryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	capture := ctx.ToolCapture()
	cmd := Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", path)
	cmd.Stdout = capture.Stdout()
	cmd.Stderr = capture.Stderr()
	return capture.Apply(cmd.Run())
}

func finishRemoteInitGitCheckout(ctx Context, projectRoot, sshKeyPath, repositoryURL string) error {
	if repositoryURL == "" {
		return errors.New("repository URL is required to finish git checkout")
	}
	if err := os.MkdirAll(projectRoot, 0o755); err != nil && !ctx.DryRun {
		return err
	}
	ctx.TraceCommand(projectRoot, "git", "clone", repositoryURL, ".")
	if ctx.DryRun {
		return nil
	}
	capture := ctx.ToolCapture()
	cmd := Command("git", "-c", "core.sshCommand="+doctorRemoteInitGitSSHCommand(sshKeyPath), "clone", repositoryURL, ".")
	cmd.Dir = projectRoot
	cmd.Stdout = capture.Stdout()
	cmd.Stderr = capture.Stderr()
	return capture.Apply(cmd.Run())
}

// finishRemoteInitGitAccess displays the SSH public key when it was
// just generated, optionally pauses for the user to import it, and
// verifies access via `git ls-remote` before doctor attempts the
// clone. Doing the verify here keeps the clone failure path narrow:
// any "Permission denied (publickey)" the user might otherwise see is
// turned into a clear, actionable message that names the key file and
// the URL.
func finishRemoteInitGitAccess(ctx Context, sshKeyPath, repositoryURL string, justGenerated bool, confirm RemoteInitFinishConfirm) error {
	if ctx.DryRun {
		ctx.TraceCommand("", "git", "-c", "core.sshCommand="+doctorRemoteInitGitSSHCommand(sshKeyPath), "ls-remote", repositoryURL, "HEAD")
		return nil
	}
	if justGenerated {
		publicKey, err := readRemoteInitPublicKey(sshKeyPath)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintln(ctx.Stdout, "Generated SSH keypair. Add the following public key to the git host that owns "+repositoryURL+" before continuing:"); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(ctx.Stdout, publicKey); err != nil {
			return err
		}
		if confirm == nil {
			return fmt.Errorf("import the SSH public key shown above on the git host, then re-run `erun doctor --finish-remote-init`")
		}
		ok, err := confirm("SSH public key imported on the git host")
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("import the SSH public key shown above on the git host, then re-run `erun doctor --finish-remote-init`")
		}
	}
	ctx.TraceCommand("", "git", "-c", "core.sshCommand="+doctorRemoteInitGitSSHCommand(sshKeyPath), "ls-remote", repositoryURL, "HEAD")
	capture := ctx.ToolCapture()
	cmd := Command("git", "-c", "core.sshCommand="+doctorRemoteInitGitSSHCommand(sshKeyPath), "ls-remote", repositoryURL, "HEAD")
	cmd.Stdout = capture.Stdout()
	cmd.Stderr = capture.Stderr()
	if err := capture.Apply(cmd.Run()); err != nil {
		publicKey, readErr := readRemoteInitPublicKey(sshKeyPath)
		if readErr == nil {
			return fmt.Errorf("git access to %s is not active yet. Confirm the following SSH public key is imported on the git host, then re-run `erun doctor --finish-remote-init`:\n%s\n%w", repositoryURL, publicKey, err)
		}
		return fmt.Errorf("git access to %s is not active yet (verified with %s). Import the corresponding public key on the git host, then re-run `erun doctor --finish-remote-init`: %w", repositoryURL, sshKeyPath, err)
	}
	return nil
}

func readRemoteInitPublicKey(sshKeyPath string) (string, error) {
	data, err := os.ReadFile(sshKeyPath + ".pub")
	if err != nil {
		return "", fmt.Errorf("read SSH public key %s.pub: %w", sshKeyPath, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func defaultRemoteInitSSHKeyPath(homeDir string) string {
	return filepath.Join(homeDir, ".ssh", "id_ed25519")
}

// doctorRemoteInitGitSSHCommand renders the ssh command git invokes via
// core.sshCommand / GIT_SSH_COMMAND. Pinning the identity stops ssh
// from falling back to whatever other private keys happen to exist in
// the user's ~/.ssh, and accept-new lets the first connection to the
// git host succeed without prompting the user to confirm a fingerprint.
func doctorRemoteInitGitSSHCommand(sshKeyPath string) string {
	return "ssh -i " + shellQuote(sshKeyPath) + " -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new"
}

// WriteRemoteInitInspectionReport renders the inspection to w in the
// shape doctor prints to the user. Returns an error iff the writer
// fails.
func WriteRemoteInitInspectionReport(ctx Context, inspection RemoteInitInspection) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Remote init checks for %s/%s:\n", remoteInitOrUnknown(inspection.Tenant), remoteInitOrUnknown(inspection.Environment)); err != nil {
		return err
	}
	markerPath := RemoteInitMarkerPath(inspection.HomeDir)
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
