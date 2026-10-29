package terminal

import (
	"encoding/base64"
	"fmt"
	"io"
	goruntime "runtime"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func (s *Service) StartInitSession(selection uimodel.Selection, cols, rows int) (uimodel.StartSessionResult, error) {
	// Init owns the env's single deploy, so it carries the MCP-auth key; a later
	// redeploy would roll the new pod.
	return s.runErunCommandInLocal(selection, cols, rows, s.d.AppendMCPAuthPublicKeyFlag(buildInitArgs(selection)))
}

// StartDeploySession installs a published version and never builds; with no
// picked version it redeploys the current one.
func (s *Service) StartDeploySession(selection uimodel.Selection, cols, rows int) (uimodel.StartSessionResult, error) {
	return s.runErunCommandInLocal(selection, cols, rows, s.d.AppendMCPAuthPublicKeyFlag(buildDeployArgs(selection)))
}

// StartCreateVersionSession builds, pushes and deploys the working tree. Only a
// local-agent env qualifies; ResolvedType is checked because a host env also
// builds here but has no pod.
func (s *Service) StartCreateVersionSession(selection uimodel.Selection, cols, rows int) (uimodel.StartSessionResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("start create-version session", selection.Tenant, selection.Environment); err != nil {
		return uimodel.StartSessionResult{}, err
	}
	if !s.d.Started() {
		return uimodel.StartSessionResult{}, fmt.Errorf("start create-version session: application context is not ready")
	}
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return uimodel.StartSessionResult{}, err
	}
	if result.EnvConfig.ResolvedType() == configpkg.EnvironmentTypeHost {
		return uimodel.StartSessionResult{}, fmt.Errorf("%s/%s is a host environment; it has no pod and no cluster to deploy to", selection.Tenant, selection.Environment)
	}
	if !result.EnvConfig.BuildsHere() || result.RemoteRepo() {
		return uimodel.StartSessionResult{}, fmt.Errorf("%s/%s has no local source to build; deploy a published version instead", selection.Tenant, selection.Environment)
	}
	go func() {
		_ = s.d.RunDeployOrchestration(s.d.Context(), selection, result, false)
	}()
	return uimodel.StartSessionResult{Selection: uimodel.NormalizeSelection(selection), Orchestrated: true}, nil
}

// StartInitialDeploySession deploys a freshly created env's first version,
// building only when the env builds here.
func (s *Service) StartInitialDeploySession(selection uimodel.Selection, cols, rows int) (uimodel.StartSessionResult, error) {
	if result, ok := s.d.MaybeStartDeployOrchestration(selection, false); ok {
		return result, nil
	}
	return s.runErunCommandInLocal(selection, cols, rows, s.d.AppendMCPAuthPublicKeyFlag(buildDeployArgs(selection)))
}

// StartForceDeploySession runs `erun deploy --force` to recover a missing image.
func (s *Service) StartForceDeploySession(selection uimodel.Selection, cols, rows int) (uimodel.StartSessionResult, error) {
	if result, ok := s.d.MaybeStartDeployOrchestration(selection, true); ok {
		return result, nil
	}
	args := s.d.AppendMCPAuthPublicKeyFlag(append(buildDeployArgs(selection), "--force"))
	return s.runErunCommandInLocal(selection, cols, rows, args)
}

// StartUpgradeEnvironmentSession runs `erun upgrade` in the env's own Local shell.
func (s *Service) StartUpgradeEnvironmentSession(selection uimodel.Selection, cols, rows int) (uimodel.StartSessionResult, error) {
	return s.runErunCommandInLocal(selection, cols, rows, buildUpgradeArgs(selection))
}

func (s *Service) StartSSHDInitSession(selection uimodel.Selection, cols, rows int) (uimodel.StartSessionResult, error) {
	return s.runErunCommandInLocal(selection, cols, rows, buildSSHDInitArgs(selection))
}

func (s *Service) StartDoctorSession(selection uimodel.Selection, cols, rows int) (uimodel.StartSessionResult, error) {
	return s.runErunCommandInLocal(selection, cols, rows, buildDoctorArgs(selection))
}

func (s *Service) runErunCommandInLocal(selection uimodel.Selection, cols, rows int, args []string) (uimodel.StartSessionResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("run erun command in local session", selection.Tenant, selection.Environment); err != nil {
		return uimodel.StartSessionResult{}, err
	}

	local, err := s.ensureLocalSession(selection, 0, cols, rows)
	if err != nil {
		return uimodel.StartSessionResult{}, err
	}

	s.mu.Lock()
	target := local.PTY
	s.mu.Unlock()
	if target == nil {
		return uimodel.StartSessionResult{}, fmt.Errorf("local session is not ready")
	}

	command := BuildLocalErunCommand(s.d.ResolveCLIPath(), args)
	if _, err := io.WriteString(target, command); err != nil {
		return uimodel.StartSessionResult{}, err
	}

	s.RecordActivity(selection)

	return uimodel.StartSessionResult{
		SessionID: local.Serial,
		Selection: selection,
		Slot:      local.Slot,
		Kind:      string(KindLocal),
	}, nil
}

func (s *Service) ensureLocalSession(selection uimodel.Selection, slot, cols, rows int) (*Session, error) {
	key := localSessionKey(selection, slot)

	s.mu.Lock()
	if existing := s.sessions[key]; existing != nil && !existing.Closed && existing.PTY != nil {
		s.mu.Unlock()
		return existing, nil
	}
	s.mu.Unlock()

	if _, err := s.StartLocalSession(selection, slot, cols, rows); err != nil {
		return nil, err
	}

	s.mu.Lock()
	managed := s.sessions[key]
	s.mu.Unlock()
	if managed == nil {
		return nil, fmt.Errorf("local session not registered after start")
	}
	return managed, nil
}

func BuildLocalErunCommand(cliPath string, args []string) string {
	return buildLocalErunCommandForOS(goruntime.GOOS, cliPath, args)
}

func buildLocalErunCommandForOS(goos, cliPath string, args []string) string {
	cliPath = strings.TrimSpace(cliPath)
	if cliPath == "" {
		cliPath = "erun"
	}
	if goos == "windows" {
		// PowerShell needs the call operator to run a quoted path, and a CR to submit:
		// PSReadLine treats LF as a continuation.
		parts := make([]string, 0, len(args)+2)
		parts = append(parts, "&", powerShellQuoteIfNeeded(cliPath))
		for _, arg := range args {
			parts = append(parts, powerShellQuoteIfNeeded(arg))
		}
		return strings.Join(parts, " ") + "\r"
	}
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, ShellQuoteIfNeeded(cliPath))
	for _, arg := range args {
		parts = append(parts, ShellQuoteIfNeeded(arg))
	}
	return strings.Join(parts, " ") + "\n"
}

// powerShellQuoteIfNeeded single-quotes value only when it is unsafe bare.
func powerShellQuoteIfNeeded(value string) string {
	if value == "" {
		return "''"
	}
	for _, r := range value {
		if !shellQuoteSafeRune(r) {
			return PowerShellQuote(value)
		}
	}
	return value
}

func ShellQuoteIfNeeded(value string) string {
	if value == "" {
		return "''"
	}
	for _, r := range value {
		if !shellQuoteSafeRune(r) {
			return ShellQuote(value)
		}
	}
	return value
}

const shellQuoteSafePunct = "-_./=+:@,"

func shellQuoteSafeRune(r rune) bool {
	switch {
	case r >= 'A' && r <= 'Z':
		return true
	case r >= 'a' && r <= 'z':
		return true
	case r >= '0' && r <= '9':
		return true
	default:
		return strings.ContainsRune(shellQuoteSafePunct, r)
	}
}

func (s *Service) StartCloudInitAWSSession(cols, rows int) (uimodel.StartSessionResult, error) {
	cols, rows = ClampSize(cols, rows)
	key := "cloud/init/aws"

	s.mu.Lock()
	if existing := s.sessions[key]; existing != nil && !existing.Closed && existing.PTY != nil {
		s.mu.Unlock()
		return uimodel.StartSessionResult{
			SessionID: existing.Serial,
			Selection: existing.Selection,
		}, nil
	}
	s.mu.Unlock()

	session, err := s.d.StartTerminal(StartParams{
		Dir:        ResolveStartDir(""),
		Executable: s.d.ResolveCLIPath(),
		Args:       buildCloudInitAWSArgs(),
		Env:        []string{s.d.AppSessionEnv},
		Cols:       cols,
		Rows:       rows,
	})
	if err != nil {
		return uimodel.StartSessionResult{}, err
	}

	s.mu.Lock()
	s.nextSerial++
	serial := s.nextSerial
	managed := &Session{
		PTY:       session,
		Key:       key,
		Serial:    serial,
		StartedAt: time.Now(),
	}
	s.sessions[key] = managed
	s.mu.Unlock()

	s.SpawnStream(managed)

	return uimodel.StartSessionResult{SessionID: serial}, nil
}

// StartCloudInitCloudflareSession runs `erun cloud init cloudflare`; the CLI owns
// the flow.
func (s *Service) StartCloudInitCloudflareSession(cols, rows int) (uimodel.StartSessionResult, error) {
	cols, rows = ClampSize(cols, rows)
	key := "cloud/init/cloudflare"

	s.mu.Lock()
	if existing := s.sessions[key]; existing != nil && !existing.Closed && existing.PTY != nil {
		s.mu.Unlock()
		return uimodel.StartSessionResult{
			SessionID: existing.Serial,
			Selection: existing.Selection,
		}, nil
	}
	s.mu.Unlock()

	session, err := s.d.StartTerminal(StartParams{
		Dir:        ResolveStartDir(""),
		Executable: s.d.ResolveCLIPath(),
		Args:       buildCloudInitCloudflareArgs(),
		Env:        []string{s.d.AppSessionEnv},
		Cols:       cols,
		Rows:       rows,
	})
	if err != nil {
		return uimodel.StartSessionResult{}, err
	}

	s.mu.Lock()
	s.nextSerial++
	serial := s.nextSerial
	managed := &Session{
		PTY:       session,
		Key:       key,
		Serial:    serial,
		StartedAt: time.Now(),
	}
	s.sessions[key] = managed
	s.mu.Unlock()

	s.SpawnStream(managed)

	return uimodel.StartSessionResult{SessionID: serial}, nil
}

func (s *Service) LogSpawnedCommandToLocal(selection uimodel.Selection, dedupKey, line string) {
	s.mu.Lock()
	var local *Session
	for _, m := range s.sessions {
		if m == nil || m.Closed || m.Kind != KindLocal {
			continue
		}
		if strings.TrimSpace(m.Selection.Tenant) != selection.Tenant ||
			strings.TrimSpace(m.Selection.Environment) != selection.Environment {
			continue
		}
		local = m
		break
	}
	if local == nil {
		s.mu.Unlock()
		return
	}
	if local.loggedCommands == nil {
		local.loggedCommands = make(map[string]struct{})
	}
	if _, ok := local.loggedCommands[dedupKey]; ok {
		s.mu.Unlock()
		return
	}
	local.loggedCommands[dedupKey] = struct{}{}
	serial := local.Serial
	s.mu.Unlock()
	s.emitEvent(uimodel.TerminalOutputEvent, uimodel.TerminalOutputPayload{
		SessionID: serial,
		Data:      base64.StdEncoding.EncodeToString([]byte(line)),
	})
}
