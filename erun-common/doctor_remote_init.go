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
	if item, ok := inspectCodeCommitSSHKey(homeDir, marker, present); ok {
		inspection.Items = append(inspection.Items, item)
	}
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

// inspectCodeCommitSSHKey reports the RSA codecommit keypair only when
// the marker explicitly records a CodeCommit host. When the marker is
// missing or did not record CodeCommit, the inspection omits this row
// — doctor falls back to detecting CodeCommit from the resolved URL
// inside RunRemoteInitFinish and generates the RSA key on the fly.
func inspectCodeCommitSSHKey(homeDir string, marker RemoteInitMarker, markerPresent bool) (RemoteInitInspectionItem, bool) {
	if !markerPresent || marker.NoGit || strings.TrimSpace(marker.CodeCommitHost) == "" {
		return RemoteInitInspectionItem{}, false
	}
	keyPath := defaultRemoteInitCodeCommitSSHKeyPath(homeDir)
	item := RemoteInitInspectionItem{
		Label: "CodeCommit SSH keypair",
		Path:  keyPath,
	}
	if _, err := os.Stat(keyPath); err == nil {
		item.Status = RemoteInitInspectionStatusOK
		return item, true
	}
	item.Status = RemoteInitInspectionStatusMissing
	return item, true
}

// RemoteInitFinishParams carries the inputs the in-runtime doctor needs
// to drive a recovery in non-dry-run mode. RepositoryURL is required
// when the marker did not record one (e.g., init was interrupted before
// the URL was resolved) and the user has not yet been prompted.
// CodeCommitSSHKeyID supplies the IAM-assigned SSH public key ID when
// the resolved repository URL points at AWS CodeCommit and the marker
// did not record one; doctor prompts only when both are missing.
//
// Sleep is the cadence the SSH-key-import polling loop uses between
// retries. When nil the production default (2s) is used; tests inject
// a recording stub so they can assert on retry behavior without
// real-time waits.
type RemoteInitFinishParams struct {
	HomeDir            string
	RepositoryURL      string
	CodeCommitSSHKeyID string
	Sleep              SleepFunc
}

// RemoteInitFinishPrompt requests one value from the caller. The CLI
// implements this with a promptui prompt; tests pass a stub. doctor
// invokes it twice when the resolved repository URL is a CodeCommit
// host and the marker has no SSH key ID: once for the repository URL,
// once for the CodeCommit SSH public key ID. Each call passes a label
// that uniquely identifies the request.
type RemoteInitFinishPrompt func(label string) (string, error)

// RunRemoteInitFinish executes whichever recovery steps the inspection
// flagged as missing. In dry-run mode it traces the steps it would run
// without performing them. Returns the updated inspection so callers
// can render a final report.
//
// The recovery mirrors `erun init --remote` for both the plain SSH
// host case (ed25519 key, ssh -i) and the AWS CodeCommit case (RSA
// 4096 key at ~/.ssh/id_rsa_codecommit, ~/.ssh/config with the IAM
// SSH key ID as user, ssh -F). doctor detects CodeCommit by parsing
// the resolved repository URL with the same parser init uses, so the
// user sees the same trace and the same poll cadence whether they're
// recovering from inside the pod (doctor) or from outside (init). The
// polling loop is implemented by WaitForGitAccess.
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

	missing := inspection.MissingItems()
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

	repository := resolveRemoteInitRepositorySpec(repositoryURL, inspection.Marker)

	if repository.CodeCommitHost != "" && !remoteInitMissingItem(missing, "CodeCommit SSH keypair") {
		rsaPath := defaultRemoteInitCodeCommitSSHKeyPath(inspection.HomeDir)
		if _, err := os.Stat(rsaPath); err != nil {
			missing = append(missing, RemoteInitInspectionItem{
				Label:  "CodeCommit SSH keypair",
				Path:   rsaPath,
				Status: RemoteInitInspectionStatusMissing,
			})
		}
	}

	sshKeyPath := defaultRemoteInitSSHKeyPath(inspection.HomeDir)
	sshKeyJustGenerated := false
	codeCommitKeyPath := ""
	codeCommitKeyJustGenerated := false
	needsGitCheckout := false
	for _, item := range orderRemoteInitMissingItems(missing) {
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
		case "CodeCommit SSH keypair":
			if err := finishRemoteInitCodeCommitSSHKey(ctx, item.Path); err != nil {
				return inspection, err
			}
			codeCommitKeyPath = item.Path
			codeCommitKeyJustGenerated = true
		case "git checkout":
			needsGitCheckout = true
		}
	}

	if needsGitCheckout {
		if repository.CodeCommitHost != "" {
			if codeCommitKeyPath == "" {
				codeCommitKeyPath = defaultRemoteInitCodeCommitSSHKeyPath(inspection.HomeDir)
			}
			if err := resolveCodeCommitSSHKeyID(ctx, &repository, codeCommitKeyPath, inspection.Tenant, inspection.Environment, params.CodeCommitSSHKeyID, prompt); err != nil {
				return inspection, err
			}
			if err := finishRemoteInitCodeCommitSSHConfig(ctx, inspection.HomeDir, repository); err != nil {
				return inspection, err
			}
		}
		if err := finishRemoteInitGitAccess(ctx, sshKeyPath, codeCommitKeyPath, repository, sshKeyJustGenerated, codeCommitKeyJustGenerated, params.Sleep); err != nil {
			return inspection, err
		}
		if err := finishRemoteInitGitCheckout(ctx, inspection.ProjectRoot, sshKeyPath, repository); err != nil {
			return inspection, err
		}
	}

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
		if err := SaveRemoteInitMarker(inspection.HomeDir, updated.Marker); err != nil {
			return inspection, err
		}
	} else {
		ctx.TraceCommand("", "write-yaml", RemoteInitMarkerPath(inspection.HomeDir))
	}
	return updated, nil
}

// resolveCodeCommitSSHKeyID fills in repository.CodeCommitSSHKeyID using
// the marker-derived value (already on the spec), then the explicit
// --codecommit-ssh-key-id flag, then an interactive prompt. Before the
// prompt fires it prints the codeCommitSetupDetails block — the RSA
// public key the user must import to IAM plus the host stanza they'll
// land in ~/.ssh/config — because IAM only returns a key ID after the
// user uploads that public key. Asking for the ID without first
// showing the key leaves the user stuck looking for a value they have
// no way to obtain. Skips silently in dry-run (no prompts) and when the
// spec already has an ID (e.g., parsed from the URL or loaded from the
// marker).
func resolveCodeCommitSSHKeyID(ctx Context, repository *remoteRepositorySpec, codeCommitKeyPath, tenant, environment, paramKeyID string, prompt RemoteInitFinishPrompt) error {
	if repository.CodeCommitSSHKeyID != "" || ctx.DryRun {
		return nil
	}
	keyID := strings.TrimSpace(paramKeyID)
	if keyID == "" {
		publicKey, err := readRemoteInitPublicKey(codeCommitKeyPath)
		if err != nil {
			return err
		}
		ctx.Info(codeCommitSetupDetails(*repository, publicKey, "<SSH public key ID>"))
		if prompt == nil {
			return errors.New("CodeCommit SSH public key ID is required to finish git checkout")
		}
		value, err := prompt(codeCommitSSHKeyIDLabel(tenant, environment))
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

// resolveRemoteInitRepositorySpec parses the resolved repository URL
// and folds in any CodeCommit metadata the marker recorded. The URL
// parser is the same one init uses, so doctor's detection stays in
// lockstep with the regex in init_remote.go. A blank URL — which can
// happen in dry-run when neither flag nor marker provides one — yields
// a zero spec, and recovery proceeds as if the repository were plain
// SSH.
func resolveRemoteInitRepositorySpec(repositoryURL string, marker RemoteInitMarker) remoteRepositorySpec {
	repositoryURL = strings.TrimSpace(repositoryURL)
	if repositoryURL == "" {
		return remoteRepositorySpec{
			CodeCommitHost:     strings.TrimSpace(marker.CodeCommitHost),
			CodeCommitSSHKeyID: strings.TrimSpace(marker.CodeCommitSSHKeyID),
		}
	}
	parsed, err := parseRemoteRepositorySpec(repositoryURL)
	if err != nil {
		return remoteRepositorySpec{URL: repositoryURL}
	}
	if parsed.CodeCommitHost != "" && parsed.CodeCommitSSHKeyID == "" {
		parsed.CodeCommitSSHKeyID = strings.TrimSpace(marker.CodeCommitSSHKeyID)
	}
	return parsed
}

func remoteInitMarkerRepositoryURL(repository remoteRepositorySpec, fallback string) string {
	if strings.TrimSpace(repository.URL) != "" {
		return repository.URL
	}
	return strings.TrimSpace(fallback)
}

// orderRemoteInitMissingItems returns missing artifacts in the order
// doctor must finish them: the project root, the ed25519 SSH keypair,
// and (when applicable) the RSA CodeCommit keypair are prerequisites
// for the git checkout, so they go first regardless of how the
// inspection records them. RunRemoteInitFinish appends a CodeCommit
// item dynamically when the resolved URL is a CodeCommit host but the
// inspection didn't record one — this helper keeps the final order
// stable in either case.
func orderRemoteInitMissingItems(items []RemoteInitInspectionItem) []RemoteInitInspectionItem {
	order := []string{"project root", "SSH keypair", "CodeCommit SSH keypair", "git checkout"}
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

// finishRemoteInitCodeCommitSSHKey generates the RSA 4096 keypair at
// ~/.ssh/id_rsa_codecommit that AWS CodeCommit requires. This mirrors
// the script init runs inside the runtime pod (init_remote.go's
// `remoteRepositoryState`), so doctor and init produce the same key
// shape.
func finishRemoteInitCodeCommitSSHKey(ctx Context, path string) error {
	ctx.TraceCommand("", "ssh-keygen", "-t", "rsa", "-b", "4096", "-N", "", "-f", path)
	if ctx.DryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	capture := ctx.ToolCapture()
	cmd := Command("ssh-keygen", "-t", "rsa", "-b", "4096", "-N", "", "-f", path)
	cmd.Stdout = capture.Stdout()
	cmd.Stderr = capture.Stderr()
	if err := capture.Apply(cmd.Run()); err != nil {
		return err
	}
	return ensureRemoteInitSSHKeyPermissions(path)
}

// finishRemoteInitCodeCommitSSHConfig writes ~/.ssh/config with the
// Host stanza CodeCommit needs (User = IAM SSH public key ID,
// IdentityFile = ~/.ssh/id_rsa_codecommit). Without this stanza, git's
// ssh process has no way to learn which IAM identity to authenticate
// as, and CodeCommit will reject the connection. The contents come
// from codeCommitSSHConfig — the same helper init uses — so the file
// is identical whether init or doctor produced it.
func finishRemoteInitCodeCommitSSHConfig(ctx Context, homeDir string, repository remoteRepositorySpec) error {
	if repository.CodeCommitHost == "" {
		return nil
	}
	configPath := filepath.Join(homeDir, ".ssh", "config")
	ctx.TraceCommand("", "write-file", configPath)
	if ctx.DryRun {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		return err
	}
	content := codeCommitSSHConfig(repository, repository.CodeCommitSSHKeyID) + "\n"
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		return err
	}
	return nil
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

func finishRemoteInitGitCheckout(ctx Context, projectRoot, sshKeyPath string, repository remoteRepositorySpec) error {
	if strings.TrimSpace(repository.URL) == "" {
		return errors.New("repository URL is required to finish git checkout")
	}
	if err := os.MkdirAll(projectRoot, 0o755); err != nil && !ctx.DryRun {
		return err
	}
	ctx.TraceCommand(projectRoot, "git", "clone", repository.URL, ".")
	if ctx.DryRun {
		return nil
	}
	capture := ctx.ToolCapture()
	cmd := Command("git", "-c", "core.sshCommand="+doctorRemoteInitGitSSHCommandFor(repository, sshKeyPath), "clone", repository.URL, ".")
	cmd.Dir = projectRoot
	cmd.Stdout = capture.Stdout()
	cmd.Stderr = capture.Stderr()
	return capture.Apply(cmd.Run())
}

// finishRemoteInitGitAccess prints the SSH public key the user must
// import on the git host, then polls `git ls-remote` until access is
// active. This mirrors init's waitForRemoteKeyImport so the user sees
// the same trace and the same poll cadence whether they're driving
// recovery from inside the pod (doctor) or from outside (init). For
// CodeCommit hosts, doctor prints the RSA codecommit public key plus
// the IAM setup snippet (same as init) and pins core.sshCommand to
// `ssh -F ~/.ssh/config`; for plain SSH hosts, doctor prints the
// ed25519 public key and pins `ssh -i <key>`.
func finishRemoteInitGitAccess(ctx Context, sshKeyPath, codeCommitKeyPath string, repository remoteRepositorySpec, sshKeyJustGenerated, codeCommitKeyJustGenerated bool, sleep SleepFunc) error {
	sshCommand := doctorRemoteInitGitSSHCommandFor(repository, sshKeyPath)
	if ctx.DryRun {
		ctx.TraceCommand("", "git", "-c", "core.sshCommand="+sshCommand, "ls-remote", repository.URL, "HEAD")
		return nil
	}
	if repository.CodeCommitHost != "" {
		if codeCommitKeyPath == "" {
			return errors.New("CodeCommit SSH key path is required to finish CodeCommit git access")
		}
		if err := ensureRemoteInitSSHKeyPermissions(codeCommitKeyPath); err != nil {
			return err
		}
		publicKey, err := readRemoteInitPublicKey(codeCommitKeyPath)
		if err != nil {
			return err
		}
		if codeCommitKeyJustGenerated {
			ctx.Info("Generated CodeCommit SSH keypair.")
		}
		ctx.Info(codeCommitSetupDetails(repository, publicKey, repository.CodeCommitSSHKeyID))
	} else {
		if err := ensureRemoteInitSSHKeyPermissions(sshKeyPath); err != nil {
			return err
		}
		publicKey, err := readRemoteInitPublicKey(sshKeyPath)
		if err != nil {
			return err
		}
		if sshKeyJustGenerated {
			ctx.Info("Generated SSH keypair. Import this SSH public key into your git host before continuing:")
		} else {
			ctx.Info("Import this SSH public key into your git host before continuing:")
		}
		ctx.Info(publicKey)
	}
	return WaitForGitAccess(ctx, sleep, func() error {
		capture := ctx.ToolCapture()
		cmd := Command("git", "-c", "core.sshCommand="+sshCommand, "ls-remote", repository.URL, "HEAD")
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

func defaultRemoteInitCodeCommitSSHKeyPath(homeDir string) string {
	return filepath.Join(homeDir, ".ssh", "id_rsa_codecommit")
}

// doctorRemoteInitGitSSHCommandFor renders the ssh command git invokes
// via core.sshCommand / GIT_SSH_COMMAND. For CodeCommit hosts, doctor
// must use `ssh -F ~/.ssh/config` so the per-host stanza (User =
// IAM SSH public key ID, IdentityFile = id_rsa_codecommit) takes
// effect — pinning a specific identity here would bypass the IAM user
// resolution and CodeCommit would reject the auth attempt. For plain
// SSH hosts we keep the existing behavior: pin the ed25519 identity
// so ssh does not fall back to whatever other keys happen to exist in
// the user's ~/.ssh, with accept-new for the first connection.
func doctorRemoteInitGitSSHCommandFor(repository remoteRepositorySpec, sshKeyPath string) string {
	if repository.CodeCommitHost != "" {
		return `ssh -F "$HOME/.ssh/config"`
	}
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
