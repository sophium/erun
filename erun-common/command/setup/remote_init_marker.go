package setup

import (
	"fmt"
	"path"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/remoteinit"
	"gopkg.in/yaml.v3"
)

// remoteInitMarkerWriteScript needs no escaping: upstream validators restrict
// the values so none can forge the closing sentinel.
func remoteInitMarkerWriteScript(marker remoteinit.RemoteInitMarker) string {
	data, err := yaml.Marshal(&marker)
	if err != nil {
		data = []byte(fmt.Sprintf("bootstrap_complete: %t\n", marker.BootstrapComplete))
	}
	// The script runs in the Linux pod, so use path, not filepath.
	relativeDir := path.Join(remoteinit.RemoteInitMarkerBaseDir, marker.Tenant, marker.Environment)
	dir := "$HOME/" + relativeDir
	target := dir + "/" + remoteinit.RemoteInitMarkerFilename
	return strings.Join([]string{
		fmt.Sprintf("mkdir -p %s", core.ShellQuote(dir)),
		fmt.Sprintf("cat > %s <<'__ERUN_BOOTSTRAP_MARKER__'", core.ShellQuote(target)),
		strings.TrimRight(string(data), "\n"),
		"__ERUN_BOOTSTRAP_MARKER__",
		fmt.Sprintf("chmod 600 %s", core.ShellQuote(target)),
	}, "\n")
}
