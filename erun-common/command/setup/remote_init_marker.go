package setup

import (
	"fmt"
	"path"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/remoteinit"
	"gopkg.in/yaml.v3"
)

// remoteInitMarkerWriteScript renders the shell snippet that writes the marker inside
// the runtime pod. No heredoc escaping is needed: tenant, environment, and repository
// values are constrained to a narrow character set upstream (parseRemoteRepositorySpec
// and the bootstrap validators), so none can forge the closing sentinel.
func remoteInitMarkerWriteScript(marker remoteinit.RemoteInitMarker) string {
	data, err := yaml.Marshal(&marker)
	if err != nil {
		data = []byte(fmt.Sprintf("bootstrap_complete: %t\n", marker.BootstrapComplete))
	}
	// Pod-side path: this script runs in the Linux runtime pod, so join with the
	// forward-slash path package, never the host's OS-native filepath (which on
	// Windows would emit .erun\<tenant>\<env>).
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
