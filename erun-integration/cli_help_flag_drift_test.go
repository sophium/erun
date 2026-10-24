package integration

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"testing"

	cmd "github.com/sophium/erun/cmd"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Every literal `erun ...` invocation in a command's Example or backticked
// Short/Long text must resolve to a real command and use only registered
// flags. Flag prose is not checked: zero-value sentinels with a prose default
// (e.g. `expose --port`) are intentional.

func walkCLICommands(root *cobra.Command, visit func(*cobra.Command)) {
	visit(root)
	for _, child := range root.Commands() {
		walkCLICommands(child, visit)
	}
}

// commandChild resolves name against Use's first word or an alias, as Cobra does.
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

// cliFlagVocabulary also includes help/h, which Cobra adds lazily.
type cliFlagVocabulary struct {
	long  map[string]bool
	short map[string]bool
}

func commandFlagVocabulary(c *cobra.Command) cliFlagVocabulary {
	// InheritedFlags merges persistent flags into c.Flags(), as cobra's help does.
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

// resolveInvocationPath collects flags before the first subcommand as
// preFlags (e.g. "erun -v deploy"); after one, a flag ends path resolution.
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

func invocationTokenTrim(tok string) string {
	return strings.Trim(tok, "\"'`();,")
}

// unknownFlagTokens stops at "--": what follows belongs to a wrapped command.
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
		if flagTokenUnknown(vocab, tok) {
			unknown = append(unknown, raw)
		}
	}
	return unknown
}

func flagTokenUnknown(vocab cliFlagVocabulary, tok string) bool {
	if strings.HasPrefix(tok, "--") {
		name := strings.TrimPrefix(tok, "--")
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			name = name[:eq]
		}
		return name != "" && !vocab.long[name]
	}
	letters := strings.TrimPrefix(tok, "-")
	if eq := strings.IndexByte(letters, '='); eq >= 0 {
		letters = letters[:eq]
	}
	for _, r := range letters {
		if !vocab.short[string(r)] {
			return true
		}
	}
	return false
}

// erunInvocationCandidates takes only the first exact "erun" token, so
// "erun-cli" and a later value literally named "erun" are not invocations.
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

func proseInvocationCandidates(text string) [][]string {
	var all [][]string
	for _, match := range backtickSpanPattern.FindAllStringSubmatch(text, -1) {
		all = append(all, erunInvocationCandidates(match[1])...)
	}
	return all
}

// auditCommandTree patches in build/push, which are omitted when cwd lacks a
// docker build context, so resolution does not depend on where tests run.
func auditCommandTree() *cobra.Command {
	root := cmd.CommandTreeForAudit()
	for name, fallback := range cmd.ContextSensitiveTopLevelCommands() {
		if commandChild(root, name) == nil {
			root.AddCommand(fallback)
		}
	}
	return root
}

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

func TestCLIExampleInvocationsUseRegisteredFlags(t *testing.T) {
	root := auditCommandTree()
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

func TestCLIProseInvocationsUseRegisteredFlags(t *testing.T) {
	root := auditCommandTree()
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
