package cloud

import (
	"fmt"
	"os"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func describeCloudContextPublicIP(ctx core.Context, deps CloudContextDependencies, provider configpkg.CloudProviderConfig, region, instanceID string) (string, error) {
	publicIP, err := deps.RunAWS(ctx, provider, region, []string{
		"ec2", "describe-instances",
		"--instance-ids", instanceID,
		"--query", "Reservations[0].Instances[0].PublicIpAddress",
		"--output", "text",
	})
	if err != nil {
		return "", err
	}
	publicIP = strings.TrimSpace(publicIP)
	if publicIP == "" || strings.EqualFold(publicIP, "none") {
		if ctx.DryRun {
			return "203.0.113.10", nil
		}
		return "", fmt.Errorf("cloud context instance %q does not have a public IP yet", instanceID)
	}
	return publicIP, nil
}

// kubectlContextConfigureExecutionOperation is the ExecutionModeFor/
// ExecutionModeReport key for the three `kubectl config set-cluster`/
// `set-credentials`/`set-context` calls configureCloudKubeContext issues to
// point a cloud-context instance's kubeconfig entry at its own k3s API
// server. Promoted for a sharper reason than the read/wait/apply cases
// before it: `set-credentials --token <adminToken>` puts a cluster-admin
// bearer token in the subprocess's own argv, readable by any co-resident
// process via /proc/<pid>/cmdline for the life of the exec. The library path
// (libraryConfigureCloudKubeContext) keeps the token in Go memory only and
// never spawns that process at all. It writes the local kubeconfig file the
// same way kubectl itself does — via k8s.io/client-go/tools/clientcmd's own
// PathOptions/ModifyConfig — so this is not a reimplementation of kubectl's
// config-writing logic, it is a reuse of it; there is no cluster API call
// and so none of kubectl-secret-apply's server-side-vs-client-side-apply
// equivalence question applies here.
const kubectlContextConfigureExecutionOperation = "kubectl-context-configure"

// cloudContextKubeconfigCommands is the single source of the three `kubectl
// config` argv sets configureCloudKubeContext renders (redacted) and, in
// subprocess mode, executes verbatim, so the trace can never drift from
// either execution path.
func cloudContextKubeconfigCommands(config configpkg.CloudContextConfig) [][]string {
	return [][]string{
		{"config", "set-cluster", config.KubernetesContext, "--server", "https://" + config.PublicIP + ":6443", "--insecure-skip-tls-verify=true"},
		{"config", "set-credentials", config.KubernetesContext, "--token", config.AdminToken},
		{"config", "set-context", config.KubernetesContext, "--cluster", config.KubernetesContext, "--user", config.KubernetesContext},
	}
}

func configureCloudKubeContext(ctx core.Context, deps CloudContextDependencies, config configpkg.CloudContextConfig) error {
	config = configpkg.NormalizeCloudContextConfig(config)
	if config.PublicIP == "" {
		return fmt.Errorf("cloud context public IP is required")
	}
	if config.AdminToken == "" {
		return fmt.Errorf("cloud context admin token is required")
	}
	commands := cloudContextKubeconfigCommands(config)
	for _, args := range commands {
		ctx.TraceCommand("", "kubectl", core.RedactRawCommandArgs(args)...)
	}
	if ctx.DryRun {
		return nil
	}
	if configpkg.CurrentExecutionMode(kubectlContextConfigureExecutionOperation) == configpkg.ExecutionModeLibrary {
		if err := libraryConfigureCloudKubeContext(config); err != nil {
			return fmt.Errorf("kubectl config context %s: %w", config.KubernetesContext, err)
		}
		return nil
	}
	for _, args := range commands {
		if err := deps.RunKubectl(ctx, args); err != nil {
			return err
		}
	}
	return nil
}

// libraryConfigureCloudKubeContext is the library-backed alternative to
// shelling out to `kubectl config set-cluster`/`set-credentials`/
// `set-context`. It loads the same kubeconfig file kubectl itself would
// (clientcmd.NewDefaultPathOptions honors KUBECONFIG/the default
// ~/.kube/config path exactly as kubectl does), updates or creates the named
// cluster/authinfo/context entries the same three fields the subprocess path
// would, and writes back through clientcmd.ModifyConfig — the same function
// kubectl's own `config set-*` subcommands call. Existing entries are
// preserved and only the fields these three commands would set are
// overwritten, matching kubectl's own per-field update behavior.
func libraryConfigureCloudKubeContext(config configpkg.CloudContextConfig) error {
	name := strings.TrimSpace(config.KubernetesContext)
	pathOptions := clientcmd.NewDefaultPathOptions()
	startingConfig, err := pathOptions.GetStartingConfig()
	if err != nil {
		return err
	}
	if startingConfig.Clusters == nil {
		startingConfig.Clusters = map[string]*clientcmdapi.Cluster{}
	}
	if startingConfig.AuthInfos == nil {
		startingConfig.AuthInfos = map[string]*clientcmdapi.AuthInfo{}
	}
	if startingConfig.Contexts == nil {
		startingConfig.Contexts = map[string]*clientcmdapi.Context{}
	}

	cluster, ok := startingConfig.Clusters[name]
	if ok {
		cluster = cluster.DeepCopy()
	} else {
		cluster = clientcmdapi.NewCluster()
	}
	cluster.Server = "https://" + config.PublicIP + ":6443"
	cluster.InsecureSkipTLSVerify = true
	startingConfig.Clusters[name] = cluster

	authInfo, ok := startingConfig.AuthInfos[name]
	if ok {
		authInfo = authInfo.DeepCopy()
	} else {
		authInfo = clientcmdapi.NewAuthInfo()
	}
	authInfo.Token = config.AdminToken
	startingConfig.AuthInfos[name] = authInfo

	kubeContext, ok := startingConfig.Contexts[name]
	if ok {
		kubeContext = kubeContext.DeepCopy()
	} else {
		kubeContext = clientcmdapi.NewContext()
	}
	kubeContext.Cluster = name
	kubeContext.AuthInfo = name
	startingConfig.Contexts[name] = kubeContext

	return clientcmd.ModifyConfig(pathOptions, *startingConfig, true)
}

func cloudContextUserDataFile(ctx core.Context, adminToken string) (string, func(), error) {
	if ctx.DryRun {
		return "<generated-k3s-user-data>", func() {}, nil
	}
	file, err := os.CreateTemp("", "erun-k3s-user-data-*.sh")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() {
		_ = os.Remove(file.Name())
	}
	userData := `#!/bin/sh
set -eu
mkdir -p /etc/rancher/k3s
cat >/etc/rancher/k3s/token-auth.csv <<'EOF'
` + adminToken + `,erun-admin,erun-admin,"system:masters"
EOF
curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="server --kube-apiserver-arg=token-auth-file=/etc/rancher/k3s/token-auth.csv" sh -
`
	if _, err := file.WriteString(userData); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return file.Name(), cleanup, nil
}
