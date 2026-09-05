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
// inside an erun runtime pod. The chart sets ERUN_ENV_TYPE on every pod it
// renders to local-agent, remote-agent, or runtime — never to host, the one
// EnvironmentType with no pod — so a valid, non-host value is true for any
// pod regardless of how its worktree is stored. ERUN_REPO_REMOTE records
// worktree storage, not pod-ness, and stays false for a local-agent pod
// (hostPath-mounted worktree) even though it runs in a pod; it is kept only
// as a fallback for a pod deployed by a chart that predates ERUN_ENV_TYPE.
func IsInRuntimeEnvironment(env func(string) string) bool {
	if env == nil {
		env = os.Getenv
	}
	if envType := EnvironmentType(strings.TrimSpace(env("ERUN_ENV_TYPE"))); envType.IsValid() {
		return envType != EnvironmentTypeHost
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
	tenant := strings.TrimSpace(env("ERUN_TENANT"))
	environment := strings.TrimSpace(env("ERUN_ENVIRONMENT"))
	marker, present, err := LoadRemoteInitMarker(homeDir, tenant, environment)
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
		inspection.Tenant = tenant
	}
	if inspection.Environment == "" {
		inspection.Environment = environment
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
	if item, ok := inspectSkills(homeDir, env); ok {
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

// bakedSkillsRoot is the runtime image's baked-in location for the canonical
// agent skills; ERUN_SKILLS_DIR overrides it in tests.
func bakedSkillsRoot(env func(string) string) string {
	if env != nil {
		if override := strings.TrimSpace(env("ERUN_SKILLS_DIR")); override != "" {
			return override
		}
	}
	return "/etc/erun/skills"
}

// inspectSkills reports whether every baked agent skill is installed into
// ~/.claude/skills (where Claude looks for them). Returns ok=false — omitting
// the row — when the image has no baked skills, so the check only surfaces
// inside a runtime pod; a plain checkout sees no skills row.
func inspectSkills(homeDir string, env func(string) string) (RemoteInitInspectionItem, bool) {
	entries, err := os.ReadDir(bakedSkillsRoot(env))
	if err != nil {
		return RemoteInitInspectionItem{}, false
	}
	installedRoot := filepath.Join(homeDir, ".claude", "skills")
	item := RemoteInitInspectionItem{Label: "agent skills", Path: installedRoot}
	anyBaked := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		anyBaked = true
		if _, err := os.Stat(filepath.Join(installedRoot, entry.Name(), "SKILL.md")); err != nil {
			item.Status = RemoteInitInspectionStatusMissing
			return item, true
		}
	}
	if !anyBaked {
		return RemoteInitInspectionItem{}, false
	}
	item.Status = RemoteInitInspectionStatusOK
	return item, true
}

// RemoteInitFinishParams carries the inputs the in-runtime doctor needs
// to drive a recovery. RepositoryURL is required when the marker did not
// record one (e.g., init was interrupted before the URL was resolved).
// CodeCommitSSHKeyID supplies the IAM-assigned SSH public key ID when the
// repository points at AWS CodeCommit and the marker did not record one;
// doctor prompts only when both are missing. Sleep is the poll cadence
// between SSH-key-import retries; nil uses the production default (2s).
type RemoteInitFinishParams struct {
	HomeDir            string
	RepositoryURL      string
	CodeCommitSSHKeyID string
	Sleep              SleepFunc
}

// RemoteInitFinishPrompt requests one value from the caller. doctor invokes
// it twice for a CodeCommit host whose marker has no SSH key ID — once for
// the repository URL, once for the CodeCommit SSH public key ID — so the
// label disambiguates the two requests.
type RemoteInitFinishPrompt func(label string) (string, error)

// RunRemoteInitFinish executes whichever recovery steps the inspection
// flagged as missing. In dry-run mode it traces the steps it would run
// without performing them. Returns the updated inspection so callers
// can render a final report.
//
// The recovery mirrors `erun init --remote` for both plain SSH and AWS
// CodeCommit hosts, using the same URL parser init uses, so the operator
// sees the same trace and poll cadence whether recovering from inside the
// pod (doctor) or outside (init).
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

func resolveRemoteInitRepositoryURL(ctx Context, inspection RemoteInitInspection, params RemoteInitFinishParams, prompt RemoteInitFinishPrompt, missing []RemoteInitInspectionItem) (string, error) {
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

func appendMissingCodeCommitKeypair(missing []RemoteInitInspectionItem, repository remoteRepositorySpec, homeDir string) []RemoteInitInspectionItem {
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

// remoteInitKeyState threads generated key paths and flags to the deferred
// git-checkout phase.
type remoteInitKeyState struct {
	sshKeyPath                 string
	sshKeyJustGenerated        bool
	codeCommitKeyPath          string
	codeCommitKeyJustGenerated bool
	needsGitCheckout           bool
}

func applyRemoteInitMissingItems(ctx Context, inspection RemoteInitInspection, missing []RemoteInitInspectionItem) (remoteInitKeyState, error) {
	state := remoteInitKeyState{sshKeyPath: defaultRemoteInitSSHKeyPath(inspection.HomeDir)}
	for _, item := range orderRemoteInitMissingItems(missing) {
		if err := applyRemoteInitMissingItem(ctx, inspection, item, &state); err != nil {
			return state, err
		}
	}
	return state, nil
}

// applyRemoteInitMissingItem materializes one missing item. The git-checkout
// case only flags needsGitCheckout — the clone runs later, after every key
// exists.
func applyRemoteInitMissingItem(ctx Context, inspection RemoteInitInspection, item RemoteInitInspectionItem, state *remoteInitKeyState) error {
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

// runRemoteInitGitCheckout clones the project using the resolved keys. It may
// update repository in place with a resolved CodeCommit SSH key ID.
func runRemoteInitGitCheckout(ctx Context, inspection RemoteInitInspection, repository *remoteRepositorySpec, state remoteInitKeyState, params RemoteInitFinishParams, prompt RemoteInitFinishPrompt) error {
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

func saveRemoteInitCompletionMarker(ctx Context, inspection RemoteInitInspection, repository remoteRepositorySpec, repositoryURL string) (RemoteInitInspection, error) {
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
		ctx.TraceCommand("", "write-yaml", RemoteInitMarkerPath(inspection.HomeDir, updated.Marker.Tenant, updated.Marker.Environment))
	}
	return updated, nil
}

// resolveCodeCommitSSHKeyID fills in repository.CodeCommitSSHKeyID from the
// flag or an interactive prompt. It prints the RSA public key and host stanza
// before prompting, because IAM only issues the key ID after the user uploads
// that public key — asking for the ID first would leave the user hunting for a
// value they cannot yet obtain.
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

// resolveRemoteInitRepositorySpec uses the same URL parser init uses, so
// doctor's CodeCommit detection stays in lockstep with init. A blank URL —
// possible in dry-run when neither flag nor marker provides one — yields a
// zero spec, and recovery proceeds as if the repository were plain SSH.
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

// orderRemoteInitMissingItems sorts missing artifacts into finish order: the
// project root and SSH keypairs are prerequisites for the git checkout, so
// they run first regardless of how the inspection recorded them.
// RunRemoteInitFinish can append a CodeCommit item dynamically, so this keeps
// the final order stable in either case.
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

func finishRemoteInitProjectRoot(ctx Context, path string) error {
	ctx.TraceCommand("", "mkdir", "-p", path)
	if ctx.DryRun {
		return nil
	}
	return os.MkdirAll(path, 0o755)
}

// finishRemoteInitSkills reinstalls the baked agent skills, mirroring the
// entrypoint so doctor can recover a pod whose skills never landed (e.g. an
// image built before the /etc/erun/skills permissions were fixed). A skill
// whose destination SKILL.md already exists is left untouched so in-env edits
// survive.
func finishRemoteInitSkills(ctx Context, homeDir string) error {
	root := bakedSkillsRoot(os.Getenv)
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, destRoot := range []string{filepath.Join(homeDir, ".claude", "skills"), filepath.Join(homeDir, ".codex", "skills")} {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			src := filepath.Join(root, entry.Name())
			dst := filepath.Join(destRoot, entry.Name())
			srcContents := src + string(os.PathSeparator) + "."
			ctx.TraceCommand("", "cp", "-R", srcContents, dst)
			if ctx.DryRun {
				continue
			}
			if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err == nil {
				continue
			}
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			capture := ctx.ToolCapture()
			cmd := Command("cp", "-R", srcContents, dst+string(os.PathSeparator))
			cmd.Stdout = capture.Stdout()
			cmd.Stderr = capture.Stderr()
			if err := capture.Apply(cmd.Run()); err != nil {
				return err
			}
		}
	}
	return nil
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

// finishRemoteInitCodeCommitSSHKey generates the RSA 4096 keypair AWS
// CodeCommit requires, mirroring init so doctor and init produce the same key
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

// finishRemoteInitCodeCommitSSHConfig writes the ~/.ssh/config Host stanza
// CodeCommit needs. Without it, git's ssh has no way to learn which IAM
// identity to authenticate as and CodeCommit rejects the connection. The
// contents come from codeCommitSSHConfig, the same helper init uses, so the
// file is identical whether init or doctor produced it.
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

// ensureRemoteInitSSHKeyPermissions re-tightens the SSH key file modes. ssh
// silently refuses a private key that is group- or world-accessible, and every
// runtime PVC that was ever mounted under the chart's former pod-wide fsGroup
// still carries the g+rw the kubelet ORed into each file on pod start. Those
// bits do not heal themselves when the fsGroup goes away, so doctor re-applies
// the expected mode every time it touches the key. The runtime entrypoint also
// heals ~/.ssh on container start, so this stays as a belt-and-braces guarantee
// for the doctor recovery path.
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
	ctx.TraceCommand(projectRoot, "git", "clone", repository.URL, ".")
	if ctx.DryRun {
		return nil
	}
	if err := os.MkdirAll(projectRoot, 0o755); err != nil {
		return err
	}
	capture := ctx.ToolCapture()
	cmd := Command("git", "-c", "core.sshCommand="+doctorRemoteInitGitSSHCommandFor(repository, sshKeyPath), "clone", repository.URL, ".")
	cmd.Dir = projectRoot
	cmd.Stdout = capture.Stdout()
	cmd.Stderr = capture.Stderr()
	return capture.Apply(cmd.Run())
}

// finishRemoteInitGitAccess prints the public key the user must import on the
// git host, then polls `git ls-remote` until access is active. This mirrors
// init's key-import wait so the operator sees the same trace and poll cadence
// whether recovering from inside the pod (doctor) or outside (init).
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

// doctorRemoteInitGitSSHCommandFor renders git's ssh command. For CodeCommit
// hosts it uses `ssh -F ~/.ssh/config` so the per-host stanza takes effect —
// pinning a specific identity would bypass IAM user resolution and CodeCommit
// would reject the auth. For plain SSH hosts it pins the ed25519 identity so
// ssh does not fall back to other keys in ~/.ssh, with accept-new for the
// first connection.
func doctorRemoteInitGitSSHCommandFor(repository remoteRepositorySpec, sshKeyPath string) string {
	if repository.CodeCommitHost != "" {
		return `ssh -F "$HOME/.ssh/config"`
	}
	return "ssh -i " + shellQuote(sshKeyPath) + " -o IdentitiesOnly=yes -o StrictHostKeyChecking=accept-new"
}

// WriteRemoteInitInspectionReport renders the inspection to ctx.Stdout in the
// shape doctor prints to the user.
func WriteRemoteInitInspectionReport(ctx Context, inspection RemoteInitInspection) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Remote init checks for %s/%s:\n", remoteInitOrUnknown(inspection.Tenant), remoteInitOrUnknown(inspection.Environment)); err != nil {
		return err
	}
	markerPath := RemoteInitMarkerPath(inspection.HomeDir, inspection.Tenant, inspection.Environment)
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
