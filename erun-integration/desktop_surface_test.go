package integration

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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

// repoRoot resolves the checkout root from this file's own location rather
// than the working directory `go test` happens to run from, so the gate finds
// erun-ui/frontend regardless of how it is invoked.
func repoRoot(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not resolve this test file's path")
	}
	// This file lives at <repoRoot>/erun-integration/desktop_surface_test.go.
	return filepath.Dir(filepath.Dir(file))
}

// readOperatorSurfaceSource concatenates every .ts/.tsx file under each given
// src root -- the committed, human-facing subtrees that together make up
// "the operator's surface": the desktop app (erun-ui/frontend/src) and the
// hosted web console (erun-console/src). Their sibling wailsjs/dist/node_modules
// directories are generated and gitignored, so a capability's binding
// showing up there is not evidence a human can reach it -- see
// erun-ui/.gitignore and erun-console/.gitignore.
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
			sb.Write(content)
			sb.WriteByte('\n')
			return nil
		})
		if err != nil {
			t.Fatalf("read %s: %v", srcDir, err)
		}
	}
	return desktopsurface.FrontendSource(sb.String())
}

// capabilityToken picks the literal token most likely to appear in whatever
// frontend code calls a capability: the last CLI path segment when one
// exists, falling further back to family, then the raw name, for a wire-only
// tool. A hyphenated leaf is collapsed to one run of letters rather than
// trimmed to its final word -- "override-advance" becomes "overrideadvance",
// "repair-org-mapping" becomes "repairorgmapping".
//
// Trimming to the final word only recovers a capability's core verb by
// accident, and this gate's value is entirely its trustworthiness. It holds
// for "override-advance", whose last word *is* the verb and whose frontend
// call site is OverrideAdvanceMergeQueue. It does not hold for
// "repair-org-mapping", whose last word is the generic noun "mapping" while
// the verb that identifies it -- "repair" -- is the part discarded. A token
// that generic identifies nothing: FrontendSource.Contains is a raw
// case-insensitive substring search over every .ts/.tsx file with no comment
// stripping, so "mapping" matched unrelated prose in both trees (a colour
// mapping named in a *.test.ts comment, an OIDC issuer mapping a form comment
// says it deliberately cannot configure) and reported a capability with no
// operator surface as surfaced -- the silent pass this derivation produced.
// Collapsing the whole leaf keeps distinct capabilities distinct, since no
// leaf can collide with another's token, while still matching the camelCase
// identifier a real call site writes: OverrideAdvanceMergeQueue lowercases to
// "overrideadvancemergequeue", which contains "overrideadvance".
func capabilityToken(name, family string, cliPath []string) string {
	token := name
	if len(cliPath) > 0 {
		token = cliPath[len(cliPath)-1]
	} else if family != "" {
		token = family
	}
	// A single-segment name or family is already one undistinguished run;
	// the hyphen is the one separator the frontend trees never write.
	return strings.ReplaceAll(token, "-", "")
}

// TestCapabilityTokenKeepsCapabilitiesDistinct pins the property the whole
// gate rests on: a capability's token has to identify that capability. Before
// this fix, capabilityToken trimmed a hyphenated leaf to its final word, so
// "platform tenant repair-org-mapping" searched for "mapping" -- a
// noun generic enough to match prose about a different mapping entirely, in a
// tree where no call site existed.
func TestCapabilityTokenKeepsCapabilitiesDistinct(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		path []string
		want string
		// callSite is the identifier a real frontend call site writes for
		// this capability. Keeping capabilities distinct must not cost a
		// genuine surface its match.
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

	// The exact prose that made the gate report an unsurfaced capability as
	// surfaced: a colour mapping named in a *.test.ts comment and a form
	// comment about an OIDC issuer mapping that form cannot configure. Neither
	// is a way in, so neither may satisfy the token.
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

// mcpCapabilities returns one Capability per registered MCP tool.
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

// mcpCoveredCLIPaths returns every CLI path (space-joined, below "erun") that
// an MCP tool already represents, so the CLI tree walk below does not
// double-count the same capability under its CLI identity too.
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

// cliOnlyCapabilities walks the real, fully-wired erun command tree and
// returns one Capability per invocable command with no matching MCP tool.
// Hidden and Deprecated commands are excluded outright: both are themselves
// explicit, reviewable markers at the command's own definition site (a
// contributor sees them in the diff that adds the command), so they need no
// second declaration here -- see erun-cli/cmd/command_tree.go.
func cliOnlyCapabilities(t testing.TB) []desktopsurface.Capability {
	t.Helper()
	covered := mcpCoveredCLIPaths()
	var capabilities []desktopsurface.Capability
	// hidden propagates down the tree: Cobra's own Hidden field only removes a
	// command from its *parent's* help listing, but a command nested under a
	// Hidden parent (erun-cli/cmd/activity/activity.go's whole "activity" group) is just
	// as undiscoverable to a human as the parent itself, so a leaf must not
	// need its own redundant Hidden to inherit that.
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

// operatorFacingCommandPaths walks the same tree cliOnlyCapabilities does and
// returns every command path whose *command* the gate holds to needing an
// operator entry point -- runnable, not Hidden or Deprecated, and not
// declared agent-facing on either the MCP or the CLI side. Those are exactly
// the commands whose flags are worth auditing: a flag on a command nobody is
// expected to reach from the desktop needs no affordance either.
//
// The tree is auditCommandTree()'s, not CommandTreeForAudit()'s directly, so
// build/push's flags are enumerated the same way regardless of the working
// directory the test binary runs from (see auditCommandTree in
// cli_help_flag_drift_test.go).
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

// agentFacingMCPToolPaths returns the CLI path of every MCP tool declared
// AgentFacing.
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

// camelCaseFlagName renders a kebab-case flag name the way a frontend request
// model spells the same field ("waiting-on-me" -> "waitingOnMe"), which is the
// other half of how a flag's dimension shows up in operator-surface source.
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

// cliFlagCapabilities returns one Capability per flag on an operator-facing
// command -- the granularity the gate was missing when it reasoned only about
// whole commands. A capability delivered as new flags on a command that
// already has a desktop surface used to pass unexamined, so a filter or
// selection dimension could ship CLI-only with nothing noticing.
//
// Only a command's own flags count: an inherited persistent flag belongs to
// the ancestor that registered it and would otherwise be re-audited once per
// descendant. Hidden flags are skipped for the same reason Hidden commands
// are -- the marker is already explicit at the registration site.
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

// resolveCommandPath walks a space-joined command path down from root.
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

// operatorConfigCapabilities returns one Capability per registry entry in
// eruncommon.OperatorSettableConfigFields -- a config field an operator is
// expected to set through some product surface, not by hand-editing
// config.yaml. Unlike the other three enumerations, there is no fully-
// automatic discovery for this source (a struct field carries no
// "operator-settable" marker to walk), so the registry itself is the
// enumeration: it exists because OrchestratorEnvConfig.Role shipped with a
// reader (`erun list`) and no writer, and none of the other three
// enumerations could see the gap because a bare config field is none of a
// route, an MCP tool, or a CLI command.
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

// TestDesktopSurfaceGate fails when a user-facing CLI command, MCP tool, API
// route, operator-settable config field, or flag on an operator-facing
// command has no way in from an operator surface -- erun-ui/frontend or
// erun-console (erun AGENTS.md § "Smooth, Seamless, No Dead Ends", failure
// mode 3). See erun-integration/AGENTS.md,
// erun-common/mcp_tools.go's AgentFacing field, erun-cli/cmd/command_tree.go's
// cliOnlyAgentFacingCommands and cliOnlyAgentFacingFlags,
// erun-backend-api/internal/routes/route_audit.go's InternalAPIRoutes, and
// erun-common/operator_settable_config.go's OperatorSettableConfigFields for
// the declaration mechanisms.
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

// capabilityNamed returns the enumerated capability with the given name, or
// fails the test -- a name that no longer resolves means the enumeration
// changed under the test, not that the assertion below is satisfied.
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

// TestDesktopSurfaceGateSeesAFilterDimensionOnAnAlreadySurfacedCommand is the
// regression test for the granularity the gate used to work at. Against an
// operator surface that references the command `erun review list` but nothing
// about its --status filter, command granularity reports no gap at all, and
// flag granularity reports the filter -- so a capability shipped as new flags
// on a command that already has a desktop surface can no longer pass
// unexamined. Review discovery's filters are the live instance that motivated
// this: the CLI grew seven of them while the gate kept asking only whether
// `review list` had a surface.
func TestDesktopSurfaceGateSeesAFilterDimensionOnAnAlreadySurfacedCommand(t *testing.T) {
	t.Parallel()

	const flagName = "review list --status"
	commandCapabilities := append(mcpCapabilities(), cliOnlyCapabilities(t)...)
	command := capabilityNamed(t, commandCapabilities, "review_list")
	flag := capabilityNamed(t, cliFlagCapabilities(), flagName)
	if flag.AgentFacing || flag.KnownGap {
		t.Fatalf("%q is declared exempt, so it cannot demonstrate the granularity change", flagName)
	}

	// References the command the way the desktop's reviews panel does, and
	// says nothing about a status filter.
	surface := desktopsurface.FrontendSource("export function ReviewsPanel() { return reviewList() }")

	if missing := desktopsurface.FindMissingDesktopSurface([]desktopsurface.Capability{command}, surface); len(missing) != 0 {
		t.Fatalf("command granularity must clear here -- that is the gap being fixed; got %+v", missing)
	}

	missing := desktopsurface.FindMissingDesktopSurface([]desktopsurface.Capability{command, flag}, surface)
	if len(missing) != 1 || missing[0].Capability.Name != flagName {
		t.Fatalf("want flag granularity to flag %q, got %+v", flagName, missing)
	}
}

// TestCLIFlagDeclarationsNameRealFlags fails when either flag registry names a
// flag the command tree no longer has. A declaration nobody can invalidate
// rots into fiction: a renamed or deleted flag would leave an exemption
// standing that silently covers nothing, and the next flag to take that name
// would inherit it.
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

// appMethodDecls parses every non-test .go file directly under erun-ui (not
// its subpackages: *App lives at the module root) and returns one
// AppMethodDecl per method on *App, with IdentUses counting how many other
// places in the same file set mention the bare method name -- a real caller,
// or nothing.
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

	// identCounts[name] counts every bare-identifier occurrence of name across
	// the whole file set, including its own declaration; FindUnboundAppMethods
	// only needs "any other use", so the declaration's own contribution
	// (exactly 1 per method, from the FuncDecl.Name identifier) is subtracted.
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

// countIdents counts every bare-identifier occurrence of each name across files.
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

// TestNoUnboundAppMethods fails when a method on *App is unexported and
// called from nowhere else in erun-ui: Wails binds only exported methods, so
// that combination is a Wails binding location with no binding -- the
// specific shape whipOrchestratorNow shipped in.
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
