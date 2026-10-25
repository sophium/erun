package helmrun

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
)

// PublishedChartNotFoundError reports a by-reference chart that could not be
// pulled, with the coordinates probed, since the cause is usually where it looked.
type PublishedChartNotFoundError struct {
	ChartReference string
	Version        string
	Registry       string
	// Candidates is empty for a component chart, which has no ladder.
	Candidates []string
	// TenantChart is empty when the tenant rides the shared chart.
	TenantChart string
	HelmOutput  string
	Err         error
}

func (e *PublishedChartNotFoundError) Error() string {
	version := strings.TrimSpace(e.Version)
	msg := "runtime chart " + strings.TrimSpace(e.ChartReference) + " version " + version + " could not be pulled"
	if registry := strings.TrimSpace(e.Registry); registry != "" {
		msg += " from " + registry
	}
	if len(e.Candidates) == 0 {
		msg += ": that version has no published chart in the registry. " +
			"`erun push` publishes a version's image and chart together, so a version is deployable only after it is pushed — " +
			"run `erun push --version " + version + "` (or `erun build --release` for a release version), then deploy."
		return msg + helmOutputSuffix(e.HelmOutput)
	}
	msg += ": no chart is published at " + version + " at any coordinate the deploy probed — " + strings.Join(e.Candidates, ", ") + ". " +
		"The " + runtimetarget.DevopsComponentName + " platform chart is published only beside the runtime image erun releases, so a registry holding just this project's own images has it at no version: " +
		"point the environment at the registry that does, with `erun init <tenant> <env> --runtime-registry <registry>`, which persists it as the env's runtimeregistry and redeploys."
	if tenantChart := strings.TrimSpace(e.TenantChart); tenantChart != "" {
		msg += " If this project publishes its own " + tenantChart + " umbrella instead, publish it at this version from the project that owns that chart — `erun push --version " + version + "` (or `erun build --release`) — then deploy."
	}
	msg += " If the environment rides a chart on another line entirely, state it outright: `runtimechart` in the env config (the desktop's Runtime tab, \"Runtime chart\") or `--runtime-chart <ref>` for one deploy, and the version keeps naming the image."
	return msg + helmOutputSuffix(e.HelmOutput)
}

func helmOutputSuffix(helmOutput string) string {
	if out := strings.TrimSpace(helmOutput); out != "" {
		return "\n" + out
	}
	return ""
}

func (e *PublishedChartNotFoundError) Unwrap() error { return e.Err }
