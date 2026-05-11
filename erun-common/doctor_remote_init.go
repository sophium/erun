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
//
// Sleep is the cadence the SSH-key-import polling loop uses between
// retries. When nil the production default (2s) is used; tests inject
// a recording stub so they can assert on retry behavior without
// real-time waits.
type RemoteInitFinishParams struct {
	HomeDir       string
	RepositoryURL string
	Sleep         SleepFunc
}

// RemoteInitFinishPrompt requests one value from the caller. The CLI
// implements this with a promptui prompt; tests pass a stub.
type RemoteInitFinishPrompt func(label string) (string, error)

// RunRemoteInitFinish executes whichever recovery steps the inspection
// flagged as missing. In dry-run mode it traces the steps it would run
// without performing them. Returns the updated inspection so callers
// can render a final report.
//
// The recovery mirrors `erun init --remote`: after generating the SSH
// keypair (or when the keypair already exists but the marker is still
// incomplete), doctor prints the public key, polls `git ls-remote` on
// a 2s cadence until access is active, and only then clones. This is
// the same flow init uses on the kubectl-driven side, so the user
// sees a consistent experience whether they're recovering from inside
// or from outside the runtime pod. The polling loop is implemented by
// WaitForGitAccess; doctor and init both call into it.
func RunRemoteInitFinish(ctx Context, inspection RemoteInitInspection, params RemoteInitFinishParams, prompt RemoteInitFinishPrompt) (RemoteInitInspection, error) {
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
			if err := finishRemoteInitGitAccess(ctx, sshKeyPath, repositoryURL, sshKeyJustGenerated, params.Sleep); err != nil {
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
	if err := capture.Apply(cmd.Run()); err != nil {
		return err
	}
	return ensureRemoteInitSSHKeyPermissions(path)
}

// ensureRemoteInitSSHKeyPermissions forces 0600 on the private key and
// 0644 on the public key. ssh silently refuses to use a private key
// that is group- or world-accessible ("WARNING: UNPROTECTED PRIVATE
// KEY FILE!" → bad permissions → key ignored), and runtime pods on
// shared PVCs often persist files with permissive group bits because
// of fsGroup or a non-077 umask. Init's chmod is best-effort and can
// be reset by the storage layer between runs, so doctor re-applies the
// expected mode every time it touches the key.
func ensureRemoteInitSSHKeyPermissions(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("chmod 0600 %s: %w", path, err)
	}
	pub := path + ".pub"
	if err := os.Chmod(pub, 0o644); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("chmod 0644 %s: %w", pub, err)
	}
	return nil
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

// finishRemoteInitGitAccess prints the SSH public key the user must
// import on the git host, then polls `git ls-remote` until access is
// active. This mirrors init's waitForRemoteKeyImport so the user sees
// the same trace and the same poll cadence whether they're driving
// recovery from inside the pod (doctor) or from outside (init).
func finishRemoteInitGitAccess(ctx Context, sshKeyPath, repositoryURL string, justGenerated bool, sleep SleepFunc) error {
	if ctx.DryRun {
		ctx.TraceCommand("", "git", "-c", "core.sshCommand="+doctorRemoteInitGitSSHCommand(sshKeyPath), "ls-remote", repositoryURL, "HEAD")
		return nil
	}
	if err := ensureRemoteInitSSHKeyPermissions(sshKeyPath); err != nil {
		return err
	}
	publicKey, err := readRemoteInitPublicKey(sshKeyPath)
	if err != nil {
		return err
	}
	if justGenerated {
		ctx.Info("Generated SSH keypair. Import this SSH public key into your git host before continuing:")
	} else {
		ctx.Info("Import this SSH public key into your git host before continuing:")
	}
	ctx.Info(publicKey)
	return WaitForGitAccess(ctx, sleep, func() error {
		capture := ctx.ToolCapture()
		cmd := Command("git", "-c", "core.sshCommand="+doctorRemoteInitGitSSHCommand(sshKeyPath), "ls-remote", repositoryURL, "HEAD")
		cmd.Stdout = capture.Stdout()
		cmd.Stderr = capture.Stderr()
		return capture.Apply(cmd.Run())
	})
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
