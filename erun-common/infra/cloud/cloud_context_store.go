package cloud

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

func saveCloudContextConfig(store CloudContextStore, context model.CloudContextConfig) error {
	context = configpkg.NormalizeCloudContextConfig(context)
	config, _, err := store.LoadERunConfig()
	if errors.Is(err, configpkg.ErrNotInitialized) {
		config = configpkg.ERunConfig{}
	} else if err != nil {
		return err
	}
	config.CloudContexts = UpsertCloudContext(config.CloudContexts, context)
	return store.SaveERunConfig(config)
}

func findCloudContext(store configpkg.CloudReadStore, name string) (model.CloudContextConfig, bool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return model.CloudContextConfig{}, false, fmt.Errorf("cloud context name is required")
	}
	contexts, err := configpkg.ListCloudContexts(store)
	if err != nil {
		return model.CloudContextConfig{}, false, err
	}
	for _, context := range contexts {
		if context.Name == name || context.KubernetesContext == name {
			return context, true, nil
		}
	}
	return model.CloudContextConfig{}, false, nil
}

func UpsertCloudContext(contexts []model.CloudContextConfig, context model.CloudContextConfig) []model.CloudContextConfig {
	context = configpkg.NormalizeCloudContextConfig(context)
	updated := false
	result := make([]model.CloudContextConfig, 0, len(contexts)+1)
	for _, existing := range contexts {
		existing = configpkg.NormalizeCloudContextConfig(existing)
		if existing.Name == "" {
			continue
		}
		if existing.Name == context.Name {
			result = append(result, context)
			updated = true
			continue
		}
		result = append(result, existing)
	}
	if !updated {
		result = append(result, context)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func validCloudContextInstanceType(value string) bool {
	for _, option := range CloudContextInstanceTypes() {
		if value == option {
			return true
		}
	}
	return false
}

func validCloudContextDiskSize(value int) bool {
	for _, option := range CloudContextDiskSizesGB() {
		if value == option {
			return true
		}
	}
	return false
}

func validCloudContextRegion(value string) bool {
	for _, option := range CloudContextRegions() {
		if value == option {
			return true
		}
	}
	return false
}

func generatedCloudContextName(provider model.CloudProviderConfig, region string, existingContexts []model.CloudContextConfig) string {
	parts := make([]string, 0, 2)
	switch {
	case provider.AccountID != "":
		parts = append(parts, provider.AccountID)
	case provider.Username != "":
		parts = append(parts, provider.Username)
	default:
		parts = append(parts, provider.Alias)
	}
	if region != "" {
		parts = append(parts, region)
	}
	tail := sanitizeCloudContextName(strings.Join(parts, "-"))
	return nextCloudContextName(tail, existingContexts)
}

func nextCloudContextName(tail string, existingContexts []model.CloudContextConfig) string {
	tail = sanitizeCloudContextName(tail)
	if tail == "" {
		tail = "context"
	}
	prefix := "erun-"
	suffix := "-" + tail
	next := 1
	for _, context := range existingContexts {
		for _, name := range []string{context.Name, context.KubernetesContext} {
			name = strings.TrimSpace(name)
			if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
				continue
			}
			counter := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
			if len(counter) != 3 {
				continue
			}
			value, err := strconv.Atoi(counter)
			if err == nil && value >= next {
				next = value + 1
			}
		}
	}
	return fmt.Sprintf("erun-%03d-%s", next, tail)
}

func sanitizeCloudContextName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func normalizeCloudContextDependencies(deps CloudContextDependencies) CloudContextDependencies {
	if deps.RunAWS == nil {
		deps.RunAWS = defaultRunCloudContextAWS
	}
	if deps.RunKubectl == nil {
		deps.RunKubectl = defaultRunCloudContextKubectl
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Sleep == nil {
		deps.Sleep = time.Sleep
	}
	if deps.NewToken == nil {
		deps.NewToken = newCloudContextToken
	}
	return deps
}

func defaultRunCloudContextAWS(ctx core.Context, provider model.CloudProviderConfig, region string, args []string) (string, error) {
	fullArgs := append([]string(nil), args...)
	if region = strings.TrimSpace(region); region != "" && !containsAWSFlag(fullArgs, "--region") {
		fullArgs = append(fullArgs, "--region", region)
	}
	if profile := strings.TrimSpace(provider.Profile); profile != "" && !containsAWSFlag(fullArgs, "--profile") {
		fullArgs = append(fullArgs, "--profile", profile)
	}
	ctx.TraceCommand("", "aws", fullArgs...)
	if ctx.DryRun {
		return dryRunAWSOutput(args), nil
	}
	var stdout bytes.Buffer
	stderr, stderrBuffer := captureWriter(ctx.Stderr)
	if err := core.RawCommandRunner("", "aws", fullArgs, nil, &stdout, stderr); err != nil {
		return "", fmt.Errorf("aws %s: %s", strings.Join(args, " "), commandErrorMessage(err, stderrBuffer.String(), "AWS command failed"))
	}
	return stdout.String(), nil
}

// defaultRunCloudContextKubectl runs a command the caller already traced and dry-run gated.
func defaultRunCloudContextKubectl(ctx core.Context, args []string) error {
	stdout, _ := captureWriter(ctx.Stdout)
	stderr, stderrBuffer := captureWriter(ctx.Stderr)
	if err := core.RawCommandRunner("", "kubectl", args, nil, stdout, stderr); err != nil {
		return fmt.Errorf("kubectl %s: %s", strings.Join(args, " "), commandErrorMessage(err, stderrBuffer.String(), "kubectl command failed"))
	}
	return nil
}

func containsAWSFlag(args []string, flag string) bool {
	for i, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
		if i > 0 && args[i-1] == flag {
			return true
		}
	}
	return false
}

func dryRunAWSOutput(args []string) string {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "ssm get-parameter"):
		return "ami-<latest-ubuntu-arm64>\n"
	case strings.Contains(joined, "ec2 create-security-group"):
		return "sg-<cloud-context>\n"
	case strings.Contains(joined, "ec2 run-instances"):
		return "i-<new-instance>\n"
	case strings.Contains(joined, "ec2 describe-instances"):
		if strings.Contains(joined, "[InstanceId,State.Name]") {
			return dryRunDescribeInstanceStateOutput(joined)
		}
		return "203.0.113.10\n"
	default:
		return ""
	}
}

// dryRunDescribeInstanceStateOutput reports "running" so dry-run preflight plans no start.
func dryRunDescribeInstanceStateOutput(joined string) string {
	const marker = "Values="
	idx := strings.Index(joined, marker)
	if idx < 0 {
		return ""
	}
	rest := joined[idx+len(marker):]
	if cut := strings.Index(rest, " "); cut >= 0 {
		rest = rest[:cut]
	}
	rest = strings.Trim(rest, "'\"")
	if rest == "" {
		return ""
	}
	var buf strings.Builder
	for _, id := range strings.Split(rest, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		buf.WriteString(id)
		buf.WriteString("\trunning\n")
	}
	return buf.String()
}

func newCloudContextToken() string {
	token := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, token); err != nil {
		return "erun-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return base64.RawURLEncoding.EncodeToString(token)
}
