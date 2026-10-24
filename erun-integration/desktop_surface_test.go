package integration

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"

	cmd "github.com/sophium/erun/cmd"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/infra/mcptools"
	"github.com/sophium/erun/erun-integration/internal/desktopsurface"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// repoRoot resolves the checkout root from this file's location, not the working directory.
func repoRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not resolve this test file's path")
	}
	return filepath.Dir(filepath.Dir(file))
}

// readOperatorSurfaceSource concatenates every .ts/.tsx file under the given src
// roots; generated wailsjs/dist/node_modules siblings are not operator surface.
func readOperatorSurfaceSource(t testing.TB, roots ...string) desktopsurface.FrontendSource {
	t.Helper()
	var sb strings.Builder
	for _, srcDir := range roots {
		err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".tsx") {
				return nil
			}
			content, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			sb.WriteString(stripTSComments(string(content)))
			sb.WriteByte('\n')
			return nil
		})
		if err != nil {
			t.Fatalf("read %s: %v", srcDir, err)
		}
	}
	return desktopsurface.FrontendSource(sb.String())
}

var (
	tsBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	tsLineComment  = regexp.MustCompile(`(?m)^\s*//.*$`)
)

// stripTSComments drops comments so only code can count as an entry point.
func stripTSComments(src string) string {
	return tsLineComment.ReplaceAllString(tsBlockComment.ReplaceAllString(src, ""), "")
}

// capabilityToken picks the token a frontend call site would contain: the last CLI
// segment, else family, else name. A hyphenated leaf collapses to one run
// ("repair-org-mapping" -> "repairorgmapping") because Contains is a raw substring
// search and a trailing word like "mapping" matches unrelated prose.
func capabilityToken(name, family string, cliPath []string) string {
	token := name
	if len(cliPath) > 0 {
		token = cliPath[len(cliPath)-1]
	} else if family != "" {
		token = family
	}
	return strings.ReplaceAll(token, "-", "")
}

func TestCapabilityTokenKeepsCapabilitiesDistinct(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		path     []string
		want     string
		callSite string
	}{
		{[]string{"review_queue", "override-advance"}, "overrideadvance", "OverrideAdvanceMergeQueue"},
		{[]string{"platform", "tenant", "repair-org-mapping"}, "repairorgmapping", "RepairOrgMapping"},
		{[]string{"platform", "tenant", "list"}, "list", "TenantList"},
	} {
		got := capabilityToken("", "", tc.path)
		if got != tc.want {
			t.Errorf("capabilityToken(%q) = %q, want %q", strings.Join(tc.path, " "), got, tc.want)
		}
		if !desktopsurface.FrontendSource("const call = " + tc.callSite + ";").Contains(got) {
			t.Errorf("capabilityToken(%q) = %q no longer matches its own call site %q",
				strings.Join(tc.path, " "), got, tc.callSite)
		}
	}

	// Prose mentioning "mapping" that is not a way in.
	unrelated := desktopsurface.FrontendSource(strings.Join([]string{
		"// rather than relying on a reviewer noticing a new hand-rolled color mapping.",
		"// because it configures an OIDC issuer mapping this form has no safe way to ...",
		"// the operator who repairs the mapping is usually the person reading this",
	}, "\n"))
	token := capabilityToken("", "", []string{"platform", "tenant", "repair-org-mapping"})
	if unrelated.Contains(token) {
		t.Errorf("capabilityToken(%q) = %q still matches unrelated prose about a different mapping",
			"platform tenant repair-org-mapping", token)
	}
}

func mcpCapabilities() []desktopsurface.Capability {
	names := mcptools.MCPToolNames()
	capabilities := make([]desktopsurface.Capability, 0, len(names))
	for _, name := range names {
		descriptor, ok := mcptools.MCPToolDescriptorFor(name)
		if !ok {
			continue
		}
		capabilities = append(capabilities, desktopsurface.Capability{
			Name:            name,
			Source:          "MCP tool",
			Token:           capabilityToken(name, descriptor.Family, descriptor.CLIPath),
			AgentFacing:     descriptor.AgentFacing,
			DeclarationHint: fmt.Sprintf("erun-common/mcp_tools.go's AgentFacing field on %q's MCPToolDescriptor entry", name),
		})
	}
	return capabilities
}

// mcpCoveredCLIPaths returns the CLI paths an MCP tool already represents, so they
// are not double-counted.
func mcpCoveredCLIPaths() map[string]bool {
	covered := make(map[string]bool)
	for _, name := range mcptools.MCPToolNames() {
		descriptor, ok := mcptools.MCPToolDescriptorFor(name)
		if !ok || len(descriptor.CLIPath) == 0 {
			continue
		}
		covered[strings.Join(descriptor.CLIPath, " ")] = true
	}
	return covered
}

// cliOnlyCapabilities returns one Capability per invocable command with no MCP
// tool; Hidden and Deprecated commands are already explicit at their definition.
func cliOnlyCapabilities(t testing.TB) []desktopsurface.Capability {
	t.Helper()
	covered := mcpCoveredCLIPaths()
	var capabilities []desktopsurface.Capability
	// A command under a Hidden parent is as undiscoverable as the parent.
	var walk func(c *cobra.Command, path []string, hidden bool)
	walk = func(c *cobra.Command, path []string, hidden bool) {
		for _, child := range c.Commands() {
			childPath := append(append([]string{}, path...), child.Name())
			childHidden := hidden || child.Hidden
			if child.Runnable() && !childHidden && child.Deprecated == "" {
				full := strings.Join(childPath, " ")
				if !covered[full] {
					capabilities = append(capabilities, desktopsurface.Capability{
						Name:            full,
						Source:          "CLI command",
						Token:           capabilityToken("", "", childPath),
						AgentFacing:     cmd.IsAgentFacingCLIOnlyCommand(full),
						DeclarationHint: fmt.Sprintf("erun-cli/cmd/command_tree.go's cliOnlyAgentFacingCommands map (add %q)", full),
					})
				}
			}
			walk(child, childPath, childHidden)
		}
	}
	walk(cmd.CommandTreeForAudit(), nil, false)
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].Name < capabilities[j].Name })
	return capabilities
}

// operatorFacingCommandPaths returns runnable, non-hidden, non-deprecated commands
// not declared agent-facing. Uses auditCommandTree so build/push flags do not
// depend on the working directory.
func operatorFacingCommandPaths() []string {
	covered := mcpCoveredCLIPaths()
	agentFacingTool := agentFacingMCPToolPaths()

	var paths []string
	var walk func(c *cobra.Command, path []string, hidden bool)
	walk = func(c *cobra.Command, path []string, hidden bool) {
		for _, child := range c.Commands() {
			childPath := append(append([]string{}, path...), child.Name())
			childHidden := hidden || child.Hidden
			full := strings.Join(childPath, " ")
			agentFacingCommand := agentFacingTool[full] ||
				(!covered[full] && cmd.IsAgentFacingCLIOnlyCommand(full))
			operatorFacing := child.Runnable() && !childHidden &&
				child.Deprecated == "" && !agentFacingCommand
			if operatorFacing {
				paths = append(paths, full)
			}
			walk(child, childPath, childHidden)
		}
	}
	walk(auditCommandTree(), nil, false)
	sort.Strings(paths)
	return paths
}

func agentFacingMCPToolPaths() map[string]bool {
	agentFacingTool := make(map[string]bool)
	for _, name := range mcptools.MCPToolNames() {
		descriptor, ok := mcptools.MCPToolDescriptorFor(name)
		if !ok || len(descriptor.CLIPath) == 0 || !descriptor.AgentFacing {
			continue
		}
		agentFacingTool[strings.Join(descriptor.CLIPath, " ")] = true
	}
	return agentFacingTool
}

// camelCaseFlagName renders a kebab-case flag the way a frontend model spells it.
func camelCaseFlagName(name string) string {
	parts := strings.Split(name, "-")
	var sb strings.Builder
	for i, part := range parts {
		if part == "" {
			continue
		}
		if i == 0 {
			sb.WriteString(part)
			continue
		}
		sb.WriteString(strings.ToUpper(part[:1]))
		sb.WriteString(part[1:])
	}
	return sb.String()
}

// cliFlagCapabilities returns one Capability per own, non-hidden flag on an
// operator-facing command; inherited flags belong to the ancestor.
func cliFlagCapabilities() []desktopsurface.Capability {
	root := auditCommandTree()
	var capabilities []desktopsurface.Capability
	for _, path := range operatorFacingCommandPaths() {
		command := resolveCommandPath(root, path)
		if command == nil {
			continue
		}
		inherited := make(map[string]bool)
		command.InheritedFlags().VisitAll(func(f *pflag.Flag) { inherited[f.Name] = true })
		command.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if f.Hidden || inherited[f.Name] {
				return
			}
			key := cmd.CLIFlagKey(path, f.Name)
			capabilities = append(capabilities, desktopsurface.Capability{
				Name:            key,
				Source:          "CLI flag",
				Tokens:          []string{f.Name, camelCaseFlagName(f.Name)},
				FlagTokens:      true,
				AgentFacing:     cmd.IsAgentFacingCLIOnlyFlag(path, f.Name),
				KnownGap:        cmd.IsKnownUnsurfacedFlag(path, f.Name),
				DeclarationHint: fmt.Sprintf("erun-cli/cmd/command_tree.go's cliOnlyAgentFacingFlags map (add %q with a comment explaining why no affordance could correspond to it)", key),
				BaselineHint:    fmt.Sprintf("erun-cli/cmd/command_tree.go's knownUnsurfacedFlags map (remove %q)", key),
			})
		})
	}
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i].Name < capabilities[j].Name })
	return capabilities
}

func resolveCommandPath(root *cobra.Command, path string) *cobra.Command {
	current := root
	for _, name := range strings.Fields(path) {
		next := commandChild(current, name)
		if next == nil {
			return nil
		}
		current = next
	}
	return current
}

// operatorConfigCapabilities returns one Capability per
// config.OperatorSettableConfigFields entry; struct fields carry no marker to walk.
func operatorConfigCapabilities() []desktopsurface.Capability {
	fields := config.OperatorSettableConfigFields
	capabilities := make([]desktopsurface.Capability, 0, len(fields))
	for _, field := range fields {
		capabilities = append(capabilities, desktopsurface.Capability{
			Name:            field.Name,
			Source:          "Operator config field",
			Token:           field.Token,
			AgentFacing:     field.Internal,
			DeclarationHint: fmt.Sprintf("erun-common/operator_settable_config.go's OperatorSettableConfigFields registry (mark %q Internal, with a reason, if it genuinely needs no operator surface)", field.Name),
		})
	}
	return capabilities
}

// TestDesktopSurfaceGate fails when a user-facing command, MCP tool, API route,
// operator-settable config field, or operator-facing flag has no operator surface.
// See erun-integration/AGENTS.md for the declaration mechanisms.
func TestDesktopSurfaceGate(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	operatorSurface := readOperatorSurfaceSource(t,
		filepath.Join(root, "erun-ui", "frontend", "src"),
		filepath.Join(root, "erun-console", "src"),
	)

	capabilities := append(mcpCapabilities(), cliOnlyCapabilities(t)...)
	capabilities = append(capabilities, apiRouteCapabilities(t, root)...)
	capabilities = append(capabilities, operatorConfigCapabilities()...)
	capabilities = append(capabilities, cliFlagCapabilities()...)
	if len(capabilities) == 0 {
		t.Fatal("found zero capabilities to audit -- the enumeration is broken, not the desktop surface")
	}

	missing := desktopsurface.FindMissingDesktopSurface(capabilities, operatorSurface)
	for _, m := range missing {
		t.Errorf("%s", m.Message())
	}

	stale := desktopsurface.FindStaleBaselineEntries(capabilities, operatorSurface)
	for _, s := range stale {
		t.Errorf("%s", s.Message())
	}
}

// capabilityNamed fails the test when name no longer resolves.
func capabilityNamed(t testing.TB, capabilities []desktopsurface.Capability, name string) desktopsurface.Capability {
	t.Helper()
	for _, c := range capabilities {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no capability named %q in the real enumeration", name)
	return desktopsurface.Capability{}
}

func TestDesktopSurfaceGateSeesAFilterDimensionOnAnAlreadySurfacedCommand(t *testing.T) {
	t.Parallel()

	const flagName = "review list --status"
	commandCapabilities := append(mcpCapabilities(), cliOnlyCapabilities(t)...)
	command := capabilityNamed(t, commandCapabilities, "review_list")
	flag := capabilityNamed(t, cliFlagCapabilities(), flagName)
	if flag.AgentFacing || flag.KnownGap {
		t.Fatalf("%q is declared exempt, so it cannot demonstrate the granularity change", flagName)
	}

	surface := desktopsurface.FrontendSource("export function ReviewsPanel() { return reviewList() }")

	if missing := desktopsurface.FindMissingDesktopSurface([]desktopsurface.Capability{command}, surface); len(missing) != 0 {
		t.Fatalf("command granularity must clear here -- that is the gap being fixed; got %+v", missing)
	}

	missing := desktopsurface.FindMissingDesktopSurface([]desktopsurface.Capability{command, flag}, surface)
	if len(missing) != 1 || missing[0].Capability.Name != flagName {
		t.Fatalf("want flag granularity to flag %q, got %+v", flagName, missing)
	}
}

// TestCLIFlagDeclarationsNameRealFlags keeps flag exemptions from outliving their flags.
func TestCLIFlagDeclarationsNameRealFlags(t *testing.T) {
	t.Parallel()
	realFlags := make(map[string]bool)
	for _, c := range cliFlagCapabilities() {
		realFlags[c.Name] = true
	}
	if len(realFlags) == 0 {
		t.Fatal("found zero CLI flag capabilities -- the enumeration is broken, not the registries")
	}
	stale := make([]string, 0)
	for _, key := range cmd.CLIFlagDeclarationKeys() {
		if !realFlags[key] {
			stale = append(stale, key)
		}
	}
	sort.Strings(stale)
	for _, key := range stale {
		t.Errorf("erun-cli/cmd/command_tree.go declares %q, but no operator-facing command registers that flag.\n"+
			"    Remove the entry, or fix the key if the flag or its command was renamed.", key)
	}
}

// appMethodDecls returns one AppMethodDecl per *App method in erun-ui's root
// package, with IdentUses counting other mentions of the name.
func appMethodDecls(t testing.TB, root string) []desktopsurface.AppMethodDecl {
	t.Helper()
	dir := filepath.Join(root, "erun-ui")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read erun-ui: %v", err)
	}

	fset := token.NewFileSet()
	files := make(map[string]*ast.File)
	for _, entry := range entries {
		if !isNonTestGoFile(entry) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		files[path] = file
	}

	// Subtract each method's own declaration.
	identCounts := countIdents(files)

	var decls []desktopsurface.AppMethodDecl
	for path, file := range files {
		for _, d := range file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || !desktopsurface.IsAppPointerReceiverMethod(fn) {
				continue
			}
			name := fn.Name.Name
			decls = append(decls, desktopsurface.AppMethodDecl{
				Name:      name,
				Exported:  desktopsurface.IsExportedName(name),
				File:      filepath.Base(path),
				Line:      fset.Position(fn.Pos()).Line,
				IdentUses: identCounts[name] - 1,
			})
		}
	}
	return decls
}

func countIdents(files map[string]*ast.File) map[string]int {
	identCounts := make(map[string]int)
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok {
				identCounts[ident.Name]++
			}
			return true
		})
	}
	return identCounts
}

// TestNoUnboundAppMethods: Wails binds only exported methods, so an unexported
// *App method with no caller is a binding that never happened.
func TestNoUnboundAppMethods(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	decls := appMethodDecls(t, root)
	if len(decls) == 0 {
		t.Fatal("found zero *App methods -- the parse is broken, not erun-ui")
	}
	for _, u := range desktopsurface.FindUnboundAppMethods(decls) {
		t.Errorf("%s", u.Message())
	}
}
