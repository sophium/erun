package doctor

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared/desktop"
	"github.com/sophium/erun/cmd/shared/prompt"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/open"
	jetbrainsconfig "github.com/sophium/erun/internal/jetbrainsconfig"
)

type jetBrainsGatewayDoctorRepair struct {
	optionsDir  string
	configID    string
	projectPath string
	idePath     string
}

func runSelectedJetBrainsGatewayRepair(ctx core.Context, promptRunner prompt.PromptRunner, result config.OpenResult, options doctorOptions) (bool, error) {
	repair, ok, err := jetBrainsGatewayDoctorRepairForResult(result)
	if err != nil {
		return false, err
	}
	if !ok {
		if options.repairJetBrainsGateway {
			_, err := fmt.Fprintln(ctx.Stdout, "No cached JetBrains Gateway backend metadata found for this environment.")
			return true, err
		}
		return false, nil
	}

	selected, err := shouldRepairJetBrainsGateway(ctx, promptRunner, result, options)
	if err != nil {
		return false, err
	}
	if !selected {
		return false, nil
	}
	return runJetBrainsGatewayRepair(ctx, repair)
}

func shouldRepairJetBrainsGateway(ctx core.Context, promptRunner prompt.PromptRunner, result config.OpenResult, options doctorOptions) (bool, error) {
	if options.repairJetBrainsGateway {
		return true, nil
	}
	if promptRunner == nil || ctx.DryRun {
		return false, nil
	}
	return doctorConfirm(ctx, promptRunner, fmt.Sprintf("Clear cached JetBrains Gateway backend metadata for %s/%s?", result.Tenant, result.Environment),
		"Re-run with --repair-jetbrains-gateway to run it without a prompt.")
}

func runJetBrainsGatewayRepair(ctx core.Context, repair jetBrainsGatewayDoctorRepair) (bool, error) {
	if _, err := fmt.Fprintf(ctx.Stdout, "Running: Clear cached JetBrains Gateway backend metadata\n"); err != nil {
		return false, err
	}
	if _, err := fmt.Fprintf(ctx.Stdout, "Cached backend path: %s\n", repair.idePath); err != nil {
		return false, err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintf(ctx.Stdout, "Would clear latest used IDE metadata in %s\n", repair.optionsDir)
		return true, err
	}
	changed, err := jetbrainsconfig.ClearRecentProjectLatestUsedIDE(repair.optionsDir, repair.configID, repair.projectPath)
	if err != nil {
		return false, err
	}
	if !changed {
		_, err := fmt.Fprintln(ctx.Stdout, "No JetBrains Gateway metadata changed.")
		return true, err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "Cleared cached JetBrains Gateway backend metadata. Open IntelliJ again to let Gateway select or redeploy the backend.")
	return true, err
}

// intelliJOptionsDir resolves this host's IntelliJ options directory; a host
// without one has no Gateway metadata to repair.
func intelliJOptionsDir() (string, bool) {
	optionsDir, err := desktop.ResolveIntelliJOptionsDir(desktop.CurrentHostOS())
	return optionsDir, err == nil
}

func jetBrainsGatewayDoctorRepairForResult(result config.OpenResult) (jetBrainsGatewayDoctorRepair, bool, error) {
	optionsDir, ok := intelliJOptionsDir()
	if !ok {
		return jetBrainsGatewayDoctorRepair{}, false, nil
	}
	info := open.SSHConnectionInfoForResult(result)
	configID := jetbrainsconfig.StableConfigID(info.HostAlias)
	projectPath := strings.TrimSpace(info.WorkspacePath)
	recent, found, err := jetbrainsconfig.FindRecentProject(optionsDir, configID, projectPath)
	if err != nil {
		return jetBrainsGatewayDoctorRepair{}, false, err
	}
	idePath := strings.TrimSpace(recent.LatestUsedIDE.PathToIDE)
	if !found || idePath == "" {
		return jetBrainsGatewayDoctorRepair{}, false, nil
	}
	return jetBrainsGatewayDoctorRepair{
		optionsDir:  optionsDir,
		configID:    configID,
		projectPath: projectPath,
		idePath:     idePath,
	}, true, nil
}

func doctorOnlySelectedJetBrainsGatewayRepair(options doctorOptions) bool {
	return options.repairJetBrainsGateway && !options.pruneImages && !options.pruneBuildCache && !options.pruneContainers
}
