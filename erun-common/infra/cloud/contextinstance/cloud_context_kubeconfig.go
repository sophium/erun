package contextinstance

import (
	"fmt"
	"os"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func DescribeCloudContextPublicIP(ctx core.Context, deps cloudcontext.CloudContextDependencies, provider model.CloudProviderConfig, region, instanceID string) (string, error) {
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

// kubectlContextConfigureExecutionOperation selects the library path, which keeps the
// admin token out of a subprocess argv (readable via /proc/<pid>/cmdline).
const kubectlContextConfigureExecutionOperation = "kubectl-context-configure"

// cloudContextKubeconfigCommands is the single source for both the redacted trace and subprocess execution.
func cloudContextKubeconfigCommands(config model.CloudContextConfig) [][]string {
	return [][]string{
		{"config", "set-cluster", config.KubernetesContext, "--server", "https://" + config.PublicIP + ":6443", "--insecure-skip-tls-verify=true"},
		{"config", "set-credentials", config.KubernetesContext, "--token", config.AdminToken},
		{"config", "set-context", config.KubernetesContext, "--cluster", config.KubernetesContext, "--user", config.KubernetesContext},
	}
}

func ConfigureCloudKubeContext(ctx core.Context, deps cloudcontext.CloudContextDependencies, config model.CloudContextConfig) error {
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

// libraryConfigureCloudKubeContext writes the same fields as the three `kubectl config set-*`
// calls via clientcmd.ModifyConfig, which kubectl itself uses.
func libraryConfigureCloudKubeContext(config model.CloudContextConfig) error {
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

func CloudContextUserDataFile(ctx core.Context, adminToken string) (string, func(), error) {
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
