package integration

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	cmd "github.com/sophium/erun/cmd"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// cli_help_flag_drift_test.go extends the doc-drift gate (see AGENTS.md
// "Doc-drift gate") to the CLI's own help text -- the surface an operator
// reads at the moment they run a destructive command. A gate-run status
// flag documented lowercase while the route accepted only uppercase, and a
// misremembered `erun release --version`, both drifted from real
// command/flag registration this way, found only by someone hitting them.
//
// What is mechanically comparable here: a flag's *name* and a command's
// *existence*. A flag's prose description (what it does, its effective
// default when the registered zero value is a sentinel) generally is not --
// see the "documented defaults" note below for why that class is left out
// rather than faked with a check that would false-positive on this
// codebase's own pervasive "empty/zero sentinel, resolved default described
// in prose" convention (e.g. `erun expose --port` registers 0 and describes
// "(default 80)"; the two are supposed to differ).
//
// So this file checks exactly two claim classes, both fully mechanical:
//  1. Every literal `erun ...` invocation quoted in a command's own Example
//     field, or backtick-quoted in any command's Short/Long prose, resolves
//     to a real subcommand path, and every `--flag`/`-f` token in it is
//     actually registered on that resolved command (TestCLIExample...,
//     TestCLIProse...). This is the exact shape of the "release --version"
//     class: a flag reachable for in prose or an example, but never wired.
//  2. The gate-run status vocabulary specifically (RUNNING/PASSED/FAILED/
//     INCONCLUSIVE): that erun-backend-api still case-normalizes every
//     entry point before comparing against that vocabulary, so the CLI's
//     own lowercase-example convention keeps working, and that the CLI's
//     own help text still mentions every value the backend accepts
//     (TestGateRunStatusVocabularyStaysMechanicallyValid). This is the
//     narrow, per-claim style TestGateRunInfrastructureSignaturesAreDocumented
//     above already established, applied to the exact vocabulary that once
//     regressed this way.
//
// What this file does NOT check, so a green run is never read as "the help
// is all true": flag *prose* (what a flag's usage string says it does or
// defaults to), whether help is *useful* (root AGENTS.md's CLI Help quality
// bar is still a human review job), and any vocabulary other than gate-run
// status. "Every registered flag appears in help" is not a separate check
// here either -- Cobra generates the flags section of `--help` directly from
// the registered FlagSet, so it holds by construction for every command
// except the one that opts out of Cobra's flag parsing (`erun exec raw`,
// DisableFlagParsing: true), which documents its one real flag (--dry-run)
// in its own Long text instead.

// walkCLICommands calls visit for every command in the tree rooted at root,
// including root itself.
func walkCLICommands(root *cobra.Command, visit func(*cobra.Command)) {
	visit(root)
	for _, child := range root.Commands() {
		walkCLICommands(child, visit)
	}
}

// commandChild finds the direct child of parent whose Use's first word, or
// an Alias, equals name -- the same resolution Cobra itself performs when
// walking a typed command line.
func commandChild(parent *cobra.Command, name string) *cobra.Command {
	for _, child := range parent.Commands() {
		if fields := strings.Fields(child.Use); len(fields) > 0 && fields[0] == name {
			return child
		}
		for _, alias := range child.Aliases {
			if alias == name {
				return child
			}
		}
	}
	return nil
}

// cliFlagVocabulary is the set of flag names and shorthands a resolved
// command actually recognizes: its own local and persistent flags, plus
// every ancestor's persistent flags, plus the "help"/"h" pair every Cobra
// command carries regardless of whether InitDefaultHelpFlag has run yet.
type cliFlagVocabulary struct {
	long  map[string]bool
	short map[string]bool
}

func commandFlagVocabulary(c *cobra.Command) cliFlagVocabulary {
	// InheritedFlags() has the side effect of merging this command's own
	// persistent flags and every ancestor's persistent flags into c.Flags()
	// (cobra's mergePersistentFlags) -- calling it first, then reading
	// c.Flags(), is the same sequence cobra's own Execute()/HelpFunc use to
	// get "every flag that applies here", so this reads exactly what
	// `erun <path> --help` would actually print.
	c.InheritedFlags()
	v := cliFlagVocabulary{long: map[string]bool{"help": true}, short: map[string]bool{"h": true}}
	c.Flags().VisitAll(func(f *pflag.Flag) {
		v.long[f.Name] = true
		if f.Shorthand != "" {
			v.short[f.Shorthand] = true
		}
	})
	return v
}

// resolveInvocationPath walks tokens[1:] (tokens[0] must be the literal
// "erun") down the real command tree as far as subcommand names go. A
// flag-looking token seen before any subcommand has matched is collected
// into preFlags (global flags like "-v"/"-vv" placed ahead of the
// subcommand name, e.g. "erun -v deploy") rather than stopping resolution --
// once at least one subcommand has matched, a flag-looking token ends path
// resolution, since a value-taking flag placed between two subcommand names
// does not occur anywhere in this codebase's help text today. Everything
// from that point on is returned as remaining, unresolved into further
// path segments, for flag-token scanning against the resolved command.
func resolveInvocationPath(root *cobra.Command, tokens []string) (resolved *cobra.Command, preFlags, remaining []string) {
	node := root
	matched := false
	i := 1
	for i < len(tokens) {
		t := tokens[i]
		if strings.HasPrefix(t, "-") {
			if matched {
				break
			}
			preFlags = append(preFlags, t)
			i++
			continue
		}
		child := commandChild(node, t)
		if child == nil {
			break
		}
		node = child
		matched = true
		i++
	}
	return node, preFlags, tokens[i:]
}

// invocationTokenTrim strips shell/quote wrapping punctuation that a token
// extracted by whitespace-splitting can pick up from surrounding shell
// syntax the tokenizer does not otherwise understand (an alias definition,
// a `trap '...'`, a `$(...)` substitution boundary).
func invocationTokenTrim(tok string) string {
	return strings.Trim(tok, "\"'`();,")
}

// unknownFlagTokens scans remaining for flag-looking tokens not present in
// vocab, stopping at a bare "--" -- Cobra's own end-of-flags marker, after
// which every following token (however it looks) belongs to a wrapped
// external command (`erun exec raw -- kubectl get pods --all-namespaces`,
// `erun exec job start ... -- ./gradlew test`), never to erun itself.
func unknownFlagTokens(vocab cliFlagVocabulary, remaining []string) []string {
	var unknown []string
	for _, raw := range remaining {
		tok := invocationTokenTrim(raw)
		if tok == "--" {
			break
		}
		if !strings.HasPrefix(tok, "-") || tok == "-" || tok == "" {
			continue
		}
		if strings.HasPrefix(tok, "--") {
			name := strings.TrimPrefix(tok, "--")
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
			}
			if name != "" && !vocab.long[name] {
				unknown = append(unknown, raw)
			}
			continue
		}
		letters := strings.TrimPrefix(tok, "-")
		if eq := strings.IndexByte(letters, '='); eq >= 0 {
			letters = letters[:eq]
		}
		if letters == "" {
			continue
		}
		allKnown := true
		for _, r := range letters {
			if !vocab.short[string(r)] {
				allKnown = false
				break
			}
		}
		if !allKnown {
			unknown = append(unknown, raw)
		}
	}
	return unknown
}

// erunInvocationCandidates finds the first occurrence of the exact token
// "erun" in a whitespace-tokenized line and returns the token sequence from
// that point to the end of the line, as a single candidate invocation (or
// none, if "erun" never appears as its own token). Requiring an exact token
// match (not a substring or prefix match) is what keeps this from firing on
// "erun-cli", "erun-common", "erunpaas.com", or "erun's": none of those
// tokenize to the bare word "erun". Taking only the *first* occurrence,
// rather than one candidate per occurrence, is what keeps this from
// misreading a later literal "erun" used as an example *value* -- a cloud
// provider type actually named "erun" (`erun cloud init erun --api-url
// ...`), or a tenant literally named "erun" (`erun list --tenant erun`) --
// as a second, nested re-invocation of the command itself.
func erunInvocationCandidates(line string) [][]string {
	fields := strings.Fields(line)
	for i, f := range fields {
		if f == "erun" {
			return [][]string{fields[i:]}
		}
	}
	return nil
}

var exampleContinuationPattern = regexp.MustCompile(`\\\n\s*`)

// exampleInvocationCandidates joins backslash-newline continuations (the
// shape every multi-line Example in this codebase uses) into one logical
// line, then extracts every candidate invocation from every non-comment
// line.
func exampleInvocationCandidates(example string) [][]string {
	joined := exampleContinuationPattern.ReplaceAllString(example, " ")
	var all [][]string
	for _, line := range strings.Split(joined, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		all = append(all, erunInvocationCandidates(line)...)
	}
	return all
}

var backtickSpanPattern = regexp.MustCompile("`([^`\n]*)`")

// proseInvocationCandidates extracts every candidate invocation from
// backtick-quoted spans in prose text (a command's Short/Long fields). A
// span that is not itself a full "erun ..." invocation (a flag mentioned
// alone, a file path, an identifier) yields no candidates, which is
// intentional: bare flag mentions with no command context are exactly the
// "prose description" class this file's own header says it does not check.
func proseInvocationCandidates(text string) [][]string {
	var all [][]string
	for _, match := range backtickSpanPattern.FindAllStringSubmatch(text, -1) {
		all = append(all, erunInvocationCandidates(match[1])...)
	}
	return all
}

// invocationProblem renders one flag-drift finding in a form that names the
// exact source (the command whose help text carries the bad reference), the
// literal text a reader would have copied, the bad token, and where
// resolution landed -- everything needed to fix it without re-deriving any
// of this test's own reasoning.
func invocationProblem(sourceCmd *cobra.Command, field string, tokens []string, resolved *cobra.Command, bad string) string {
	return fmt.Sprintf("%s: %s %q references %s, which is not a registered flag on %q (or any of its parents)",
		sourceCmd.CommandPath(), field, strings.Join(tokens, " "), bad, resolved.CommandPath())
}

func checkInvocation(root *cobra.Command, sourceCmd *cobra.Command, field string, tokens []string, problems *[]string) {
	resolved, preFlags, remaining := resolveInvocationPath(root, tokens)
	for _, bad := range unknownFlagTokens(commandFlagVocabulary(root), preFlags) {
		*problems = append(*problems, invocationProblem(sourceCmd, field, tokens, root, bad))
	}
	for _, bad := range unknownFlagTokens(commandFlagVocabulary(resolved), remaining) {
		*problems = append(*problems, invocationProblem(sourceCmd, field, tokens, resolved, bad))
	}
}

// TestCLIExampleInvocationsUseRegisteredFlags catches the exact "erun
// release --version" class of drift: a flag copy-pasted into a command's
// own Example block (the literal text `erun <cmd> --help` prints) that was
// never actually registered on the command the example invokes, or on any
// command it passes through on the way there.
func TestCLIExampleInvocationsUseRegisteredFlags(t *testing.T) {
	root := cmd.CommandTreeForAudit()
	var problems []string
	seen := 0
	walkCLICommands(root, func(c *cobra.Command) {
		for _, tokens := range exampleInvocationCandidates(c.Example) {
			seen++
			checkInvocation(root, c, "Example", tokens, &problems)
		}
	})
	if seen == 0 {
		t.Fatal("no \"erun ...\" invocations found in any command's Example field; the rest of this test would pass vacuously")
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

// TestCLIProseInvocationsUseRegisteredFlags applies the same check to
// backtick-quoted invocations inside Short/Long prose (e.g. "`erun deploy
// --version <version>`" in another command's Long text) -- the second place
// a stale or wrong flag reference reaches a reader, distinct from Example
// because these are cross-references between commands rather than a
// command's own worked example.
func TestCLIProseInvocationsUseRegisteredFlags(t *testing.T) {
	root := cmd.CommandTreeForAudit()
	var problems []string
	seen := 0
	walkCLICommands(root, func(c *cobra.Command) {
		for _, tokens := range proseInvocationCandidates(c.Short + "\n" + c.Long) {
			seen++
			checkInvocation(root, c, "Short/Long", tokens, &problems)
		}
	})
	if seen == 0 {
		t.Fatal("no backtick-quoted \"erun ...\" invocations found in any command's Short/Long text; the rest of this test would pass vacuously")
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
}

// findCLICommand resolves a space-joined command path (e.g. "exec gate-run
// report") against the real tree, the same key shape
// cliOnlyAgentFacingCommands uses.
func findCLICommand(root *cobra.Command, path string) *cobra.Command {
	node := root
	for _, name := range strings.Fields(path) {
		node = commandChild(node, name)
		if node == nil {
			return nil
		}
	}
	return node
}

// gateRunStatusConstsFromModel parses erun-backend-api/internal/model/gate_run.go
// (a separate Go module, so read as source rather than imported -- the same
// cross-module technique desktop_surface_test.go's apiRouteCapabilities
// uses) for the string literal values of the GateRunStatus typed constants:
// the actual, code-owned status vocabulary a gate run can hold.
func gateRunStatusConstsFromModel(t *testing.T, root string) []string {
	t.Helper()
	path := filepath.Join(root, "erun-backend", "erun-backend-api", "internal", "model", "gate_run.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var values []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		typeIdent, ok := spec.Type.(*ast.Ident)
		if !ok || typeIdent.Name != "GateRunStatus" {
			return true
		}
		for _, v := range spec.Values {
			lit, ok := v.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			if s, err := strconv.Unquote(lit.Value); err == nil {
				values = append(values, s)
			}
		}
		return true
	})
	return values
}

// TestGateRunStatusVocabularyStaysMechanicallyValid is the per-claim doc-drift
// check for a vocabulary that has already drifted this way once: a gate run's
// --status value is case-sensitive end to end unless erun-backend-api
// normalizes it, and the CLI's own help/Example text has taught a lowercase
// convention since before the fix landed. This does not re-verify the fix by
// calling the API (that is erun-backend-api's own regression-test job); it
// verifies the two facts that made the fix correct and would catch either
// half regressing: the normalization is still present at every entry point,
// and the CLI still mentions every value the backend actually accepts.
func TestGateRunStatusVocabularyStaysMechanicallyValid(t *testing.T) {
	root := repoRoot(t)
	statuses := gateRunStatusConstsFromModel(t, root)
	if len(statuses) == 0 {
		t.Fatal("no GateRunStatus constants found in erun-backend-api/internal/model/gate_run.go; the rest of this test would pass vacuously")
	}

	routesPath := filepath.Join(root, "erun-backend", "erun-backend-api", "internal", "routes", "gate_runs.go")
	data, err := os.ReadFile(routesPath)
	if err != nil {
		t.Fatalf("read %s: %v", routesPath, err)
	}
	const normalizeCall = "strings.ToUpper(strings.TrimSpace("
	if count := strings.Count(string(data), normalizeCall); count < 3 {
		t.Errorf("%s: expected case-normalization (%s...)) at all three gate-run status entry points -- "+
			"list, start, report -- so the CLI's lowercase --status examples keep working; found %d call(s), want at least 3",
			routesPath, normalizeCall, count)
	}

	cliRoot := cmd.CommandTreeForAudit()
	var cliText strings.Builder
	for _, path := range []string{"exec gate-run start", "exec gate-run report", "gate list"} {
		c := findCLICommand(cliRoot, path)
		if c == nil {
			t.Errorf("erun %s: command not found in the real command tree; the CLI side of this check cannot run", path)
			continue
		}
		cliText.WriteString(c.Long)
		cliText.WriteString("\n")
		cliText.WriteString(c.Example)
		cliText.WriteString("\n")
		c.Flags().VisitAll(func(f *pflag.Flag) {
			cliText.WriteString(f.Usage)
			cliText.WriteString("\n")
		})
	}
	words := normalizedWordSet(cliText.String())
	var missing []string
	for _, status := range statuses {
		if !words[strings.ToLower(status)] {
			missing = append(missing, status)
		}
	}
	sort.Strings(missing)
	for _, status := range missing {
		t.Errorf("gate-run status %q exists in erun-backend-api's GateRunStatus but is not mentioned anywhere in "+
			"`erun exec gate-run start`/`report` or `erun gate list`'s own help text -- update the CLI's --status "+
			"flag usage, Long, or Example", status)
	}
}
