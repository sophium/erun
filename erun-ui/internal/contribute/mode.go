package contribute

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/localports"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

type State struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Enabled     bool   `json:"enabled"`
}

type AppLaunch struct {
	URL  string `json:"url"`
	Port int    `json:"port"`
}

const ChangedEvent = "contribute:changed"

// reservedTenant owns ERun itself; contributing from it would be self-referential.
const reservedTenant = "erun"

func (s *Service) IsEligible(selection uimodel.Selection) bool {
	selection = uimodel.NormalizeSelection(selection)
	if selection.Tenant == "" || selection.Environment == "" {
		return false
	}
	if strings.EqualFold(selection.Tenant, reservedTenant) {
		return false
	}
	envConfig, _, err := s.d.Store.LoadEnvConfig(selection.Tenant, selection.Environment)
	if err != nil {
		return false
	}
	switch envConfig.Type {
	case config.EnvironmentTypeLocalAgent, config.EnvironmentTypeRemoteAgent:
		return true
	case config.EnvironmentTypeRuntime, config.EnvironmentTypeHost:
		fallthrough
	default:
		return false
	}
}

func (s *Service) Mode(selection uimodel.Selection) bool {
	return s.flags.get(selection)
}

// SetMode toggles contribute mode, cloning ERun first when enabling.
// The frontend manages the contribute tabs.
func (s *Service) SetMode(selection uimodel.Selection, on bool) (State, error) {
	selection = uimodel.NormalizeSelection(selection)
	if err := uimodel.ErrMissingTenantOrEnvironment("set contribute mode", selection.Tenant, selection.Environment); err != nil {
		return State{}, err
	}
	if on {
		if !s.IsEligible(selection) {
			return State{}, fmt.Errorf("contribute mode is only available for non-erun local-agent and remote-agent environments")
		}
		if err := s.ensureERunClone(selection); err != nil {
			return State{}, err
		}
	} else {
		s.closeSessionsForSelection(selection)
		s.stopAppForward(selection)
	}
	s.flags.set(selection, on)
	if err := s.flags.saveToDisk(); err != nil {
		return State{}, fmt.Errorf("persist contribute state: %w", err)
	}
	state := State{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Enabled:     on,
	}
	s.d.Emit(ChangedEvent, state)
	return state, nil
}

func (s *Service) ensureERunClone(selection uimodel.Selection) error {
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return fmt.Errorf("resolve environment: %w", err)
	}
	ctx := s.d.Context()
	mcpPort := localports.MCPPortForResult(result)
	if !s.d.CanReachMCPEndpoint(mcpPort) {
		return s.d.WrapMCPUnreachable(fmt.Errorf("mcp port %d is not reachable; open the environment first", mcpPort))
	}
	endpoint := s.d.MCPEndpoint(result)
	if err := s.d.CloneERun(ctx, endpoint, s.d.MCPBearer(result.Tenant, result.EnvConfig.Name)); err != nil {
		if s.d.IsMCPDialFailure(err) {
			return s.d.WrapMCPUnreachable(err)
		}
		return fmt.Errorf("clone ERun: %w", err)
	}
	return nil
}

func (s *Service) resolveAppPort(selection uimodel.Selection) (int, error) {
	if err := uimodel.ErrMissingTenantOrEnvironment("resolve contribute app port", selection.Tenant, selection.Environment); err != nil {
		return 0, err
	}
	if !s.Mode(selection) {
		return 0, fmt.Errorf("contribute mode is not enabled for %s/%s", selection.Tenant, selection.Environment)
	}
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return 0, fmt.Errorf("resolve environment: %w", err)
	}
	port := localports.ContributeAppPortForResult(result)
	if port <= 0 {
		return 0, fmt.Errorf("contribute-app port is not allocated for %s/%s", selection.Tenant, selection.Environment)
	}
	return port, nil
}

// StartApp runs the headless app in the contribute tab and returns its URL
// once it answers; opening it in a browser is the caller's.
func (s *Service) StartApp(selection uimodel.Selection) (AppLaunch, error) {
	selection = uimodel.NormalizeSelection(selection)
	port, err := s.resolveAppPort(selection)
	if err != nil {
		return AppLaunch{}, err
	}

	// Run in the contribute tab so the user sees the build and can Ctrl-C it. Best-effort.
	s.sendCommandToERunSession(selection, appLaunchCommand(port))

	ctx := s.d.Context()
	forward, args, localPort, err := s.startAppForward(ctx, selection)
	if err != nil {
		return AppLaunch{}, err
	}
	if forward != nil {
		s.forwards.put(selection, forward)
	}
	if err := waitForAppReachable(ctx, localPort, forward, args); err != nil {
		s.stopAppForward(selection)
		return AppLaunch{}, err
	}
	return AppLaunch{
		URL:  fmt.Sprintf("http://127.0.0.1:%d/", localPort),
		Port: localPort,
	}, nil
}

func (s *Service) stopAppForward(selection uimodel.Selection) {
	selection = uimodel.NormalizeSelection(selection)
	if forward := s.forwards.take(selection); forward != nil {
		forward.stop()
	}
	// Ctrl-C stops the headless process without killing the shell.
	s.sendCommandToERunSession(selection, "\x03")
}

// The clone's development wrapper rebuilds the desktop before `erun app`.
func appLaunchCommand(port int) string {
	return fmt.Sprintf("erun app --headless --port %d\n", port)
}

func (s *Service) sendCommandToERunSession(selection uimodel.Selection, command string) {
	s.d.Sessions.WriteToFirstWithKeyPrefix(sessionKeyPrefix(KindERun, selection), command)
}
