package cmd

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	common "github.com/sophium/erun/erun-common"
)

// previewAdoptOrConflict surfaces in dry-run what the live path would do
// when the local port is already held: adopt the existing kubectl
// port-forward (when argv matches expected) or refuse with a holder
// description. Returns previewed=true when a holder was identified and a
// trace was emitted, in which case the caller should short-circuit and
// return that port. previewed=false means no holder was identified and
// the caller should fall back to the normal "trace kubectl args" path.
//
// The probe is gated on lsof being available (findLocalPortHolder
// returns false otherwise), so platforms without lsof keep today's
// behaviour: a kubectl trace line and an assumed-fresh-start plan.
func previewAdoptOrConflict(ctx common.Context, kind string, localPort int, expectedArgs []string) (bool, int) {
	// We deliberately do not gate on canConnectLocalPort here: lsof is
	// the source of truth for "is there a TCP listener", and gating on a
	// separate Dial would make the probe sensitive to transient connect
	// races. When nothing is listening lsof exits non-zero, so the probe
	// stays silent without any extra check.
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
// invocation. We only carry the fields that participate in adopt-or-conflict
// identity decisions: namespace, the target spec (e.g.
// `deployment/erun-devops` or `service/erun-api`), the local:remote port
// mapping, and the optional bind address. context is kept for richer error
// messages but is not part of the strict identity comparison — the
// kubernetes context lives on the calling host and we want adoption to
// succeed even if a user invoked kubectl with a different `--context`
// alias that happens to resolve to the same cluster.
type kubectlPortForwardArgv struct {
	Binary    string
	Context   string
	Namespace string
	Target    string
	PortPair  string
	Address   string
}

// parseKubectlPortForwardArgv tries to interpret an argv slice as a
// `kubectl port-forward` invocation. Returns the parsed form and true when
// the argv matches that shape. The binary token is allowed to be any path
// whose basename starts with "kubectl" so that version-managed launchers
// (`kuberlr`'s `kubectl1.32.0`, `kubectx`'s wrappers, etc.) still adopt.
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

// consumeKubectlPortForwardFlag matches one of the kubectl flags whose
// value participates in identity comparison (--context, --namespace/-n,
// --address) at argv[*i]. On a match the parsed value is written into
// out, *i is advanced past the consumed token(s), and consumed=true is
// returned. Tokens we don't recognise leave *i untouched.
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

// matchKubectlFlag reports whether argv[i] is one of the given flag names,
// returning the parsed value. advance is the number of argv tokens
// consumed: 1 for `--flag=value`, 2 for `--flag value`, 0 when the flag
// matched but is missing its value (caller treats this as a malformed
// flag we skip).
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
// `kubectl port-forward` invocation describes the same target erun would
// start itself. Mismatch on namespace, target, or port pair is a hard "do
// not adopt" — those are the fields whose drift indicates a different
// tenant, environment, or service. Context and address are intentionally
// not part of the equality check; see kubectlPortForwardArgv.Context for
// the rationale.
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

// formatHolderForError produces a short, human-readable description of the
// process currently holding a port erun wanted to bind. Used to enrich the
// "already in use" error so the user sees what they need to kill instead
// of a bare port number.
func formatHolderForError(pid int, argv []string) string {
	command := strings.Join(argv, " ")
	if command == "" {
		return ""
	}
	return "PID " + strconv.Itoa(pid) + ": " + command
}
