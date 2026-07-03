package cmd

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	common "github.com/sophium/erun/erun-common"
)

// previewAdoptOrConflict mirrors in dry-run what the live path would do
// when the local port is already held: adopt the existing kubectl
// port-forward or refuse. previewed=true means a holder was found and the
// caller should short-circuit on the returned port; previewed=false means
// fall back to the normal kubectl-args trace. Without lsof no holder is
// ever found, so those platforms keep the old assumed-fresh-start plan.
func previewAdoptOrConflict(ctx common.Context, kind string, localPort int, expectedArgs []string) (bool, int) {
	// Don't add a canConnectLocalPort/Dial gate here: lsof alone is
	// authoritative for "is anything listening", and a separate Dial would
	// make the probe sensitive to transient connect races.
	pid, argv, ok := findLocalPortHolder(localPort)
	if !ok {
		return false, 0
	}
	if argvMatchesExpectedKubectlPortForward(argv, expectedArgs) {
		ctx.Trace(fmt.Sprintf("%s: would adopt existing kubectl port-forward on 127.0.0.1:%d (PID %d)", kind, localPort, pid))
		return true, localPort
	}
	ctx.TraceCommand("", "kubectl", expectedArgs...)
	ctx.Trace(fmt.Sprintf("%s: would refuse to bind 127.0.0.1:%d — held by %s", kind, localPort, formatHolderForError(pid, argv)))
	return true, 0
}

// kubectlPortForwardArgv is a parsed view of a `kubectl ... port-forward ...`
// invocation, carrying only the fields used for adopt-or-conflict identity.
// Context is deliberately excluded from that comparison: a different
// `--context` alias can resolve to the same cluster, and adoption should
// still succeed.
type kubectlPortForwardArgv struct {
	Binary    string
	Context   string
	Namespace string
	Target    string
	PortPair  string
	Address   string
}

// parseKubectlPortForwardArgv interprets an argv slice as a
// `kubectl port-forward` invocation. The binary token matches any path
// whose basename starts with "kubectl" so version-managed launchers
// (`kuberlr`'s `kubectl1.32.0`, `kubectx` wrappers) still adopt.
func parseKubectlPortForwardArgv(argv []string) (kubectlPortForwardArgv, bool) {
	if len(argv) == 0 || !strings.HasPrefix(filepath.Base(argv[0]), "kubectl") {
		return kubectlPortForwardArgv{}, false
	}
	out := kubectlPortForwardArgv{Binary: argv[0]}
	sawPortForward := false
	positionals := make([]string, 0, 4)
	for i := 1; i < len(argv); i++ {
		token := argv[i]
		if token == "port-forward" {
			sawPortForward = true
			continue
		}
		if consumed := consumeKubectlPortForwardFlag(argv, &i, &out); consumed {
			continue
		}
		if strings.HasPrefix(token, "-") {
			continue
		}
		positionals = append(positionals, token)
	}
	if !sawPortForward || len(positionals) < 2 {
		return kubectlPortForwardArgv{}, false
	}
	out.Target = positionals[0]
	out.PortPair = positionals[1]
	return out, true
}

func consumeKubectlPortForwardFlag(argv []string, i *int, out *kubectlPortForwardArgv) bool {
	for _, flag := range kubectlPortForwardIdentityFlags {
		value, advance, matched := matchKubectlFlag(argv, *i, flag.names)
		if !matched {
			continue
		}
		if advance > 0 {
			flag.assign(out, value)
			*i += advance - 1
		}
		return true
	}
	return false
}

type kubectlPortForwardIdentityFlag struct {
	names  []string
	assign func(*kubectlPortForwardArgv, string)
}

var kubectlPortForwardIdentityFlags = []kubectlPortForwardIdentityFlag{
	{names: []string{"--context"}, assign: func(o *kubectlPortForwardArgv, v string) { o.Context = v }},
	{names: []string{"--namespace", "-n"}, assign: func(o *kubectlPortForwardArgv, v string) { o.Namespace = v }},
	{names: []string{"--address"}, assign: func(o *kubectlPortForwardArgv, v string) { o.Address = v }},
}

func matchKubectlFlag(argv []string, i int, names []string) (value string, advance int, matched bool) {
	token := argv[i]
	for _, name := range names {
		if token == name {
			if i+1 >= len(argv) {
				return "", 0, true
			}
			return argv[i+1], 2, true
		}
		if strings.HasPrefix(token, name+"=") {
			return strings.TrimPrefix(token, name+"="), 1, true
		}
	}
	return "", 0, false
}

// argvMatchesExpectedKubectlPortForward decides whether a foreign
// `kubectl port-forward` describes the same target erun would start itself.
// Namespace, target, or port-pair drift means a different tenant,
// environment, or service, so it is a hard "do not adopt". Context and
// address are intentionally excluded; see kubectlPortForwardArgv.Context.
func argvMatchesExpectedKubectlPortForward(actual []string, expected []string) bool {
	a, ok := parseKubectlPortForwardArgv(actual)
	if !ok {
		return false
	}
	e, ok := parseKubectlPortForwardArgv(append([]string{"kubectl"}, expected...))
	if !ok {
		return false
	}
	return a.Namespace == e.Namespace && a.Target == e.Target && a.PortPair == e.PortPair
}

// formatHolderForError describes the process holding a port erun wanted to
// bind, so the "already in use" error tells the user what to kill instead
// of a bare port number.
func formatHolderForError(pid int, argv []string) string {
	command := strings.Join(argv, " ")
	if command == "" {
		return ""
	}
	return "PID " + strconv.Itoa(pid) + ": " + command
}
