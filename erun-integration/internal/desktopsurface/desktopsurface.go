// Package desktopsurface implements the classifier behind
// TestDesktopSurfaceGate: it flags a user-facing CLI command or MCP tool that
// has no way in from the desktop app (erun AGENTS.md § "Smooth, Seamless, No
// Dead Ends", failure mode 3), and separately flags a Wails binding location
// with no binding -- an unexported *App method no other Go code calls.
package desktopsurface

import (
	"fmt"
	"go/ast"
	"sort"
	"strings"
)

// Capability is one user-reachable action the CLI or MCP surface exposes.
type Capability struct {
	// Name identifies the capability for reporting: an MCP tool name, or a
	// CLI command path joined with spaces (e.g. "app restart").
	Name string
	// Source names where the capability comes from, for the failure message.
	Source string
	// Token is searched for, case-insensitively, as a plain substring of
	// erun-ui/frontend/src. It is deliberately not word-bounded: a capability
	// name embedded in a camelCase identifier (WhipButton, renderWhipPanel)
	// must still count as a match.
	Token string
	// AgentFacing marks a capability declared exempt from needing a desktop
	// entry point. See eruncommon.MCPToolDescriptor.AgentFacing and
	// erun-cli/cmd.cliOnlyAgentFacingCommands for the two declaration sites.
	AgentFacing bool
}

// Missing is a capability the gate could not find any desktop entry point
// for.
type Missing struct {
	Capability Capability
}

// Message names the gap and where to close it, so the gate's failure is
// itself a next action rather than a dead end (erun AGENTS.md § "Smooth,
// Seamless, No Dead Ends": "the check must name what is missing and where to
// add it, not merely fail").
func (m Missing) Message() string {
	return fmt.Sprintf(
		"%s %q has no desktop entry point: erun-ui/frontend/src contains no reference to %q.\n"+
			"    Either add a way to reach it from erun-ui/frontend/src, or if it is genuinely "+
			"agent-only, declare that explicitly (erun-common/mcp_tools.go's AgentFacing field for "+
			"an MCP tool, or erun-cli/cmd/command_tree.go's cliOnlyAgentFacingCommands for a CLI-only command).",
		m.Capability.Source, m.Capability.Name, m.Capability.Token,
	)
}

// FrontendSource is the concatenated text of every frontend source file the
// gate searches. Callers build this from erun-ui/frontend/src so the
// classifier itself stays free of filesystem access and is easy to unit test.
type FrontendSource string

// Contains reports whether the frontend source references a token anywhere,
// case-insensitively.
func (s FrontendSource) Contains(token string) bool {
	return strings.Contains(strings.ToLower(string(s)), strings.ToLower(token))
}

// FindMissingDesktopSurface returns every non-agent-facing capability whose
// token is absent from frontendSource, ordered by name for a stable report.
func FindMissingDesktopSurface(capabilities []Capability, frontendSource FrontendSource) []Missing {
	var missing []Missing
	for _, c := range capabilities {
		if c.AgentFacing {
			continue
		}
		if frontendSource.Contains(c.Token) {
			continue
		}
		missing = append(missing, Missing{Capability: c})
	}
	sort.Slice(missing, func(i, j int) bool {
		return missing[i].Capability.Name < missing[j].Capability.Name
	})
	return missing
}

// UnboundAppMethod is a method on *App that Wails would silently skip when
// generating bindings: unexported, so the binding generator never sees it,
// and referenced by no other Go code in erun-ui, so nothing calls it either.
// That combination is the whip shape (erun#1433): the author placed the
// method where a binding belongs, got no binding, and got no error.
type UnboundAppMethod struct {
	Name string
	File string
	Line int
}

// Message names the method, where it lives, and the fix.
func (m UnboundAppMethod) Message() string {
	return fmt.Sprintf(
		"%s:%d: (*App).%s is unexported and called from nowhere else in erun-ui.\n"+
			"    Wails only binds exported methods on *App, so this method has no generated binding and\n"+
			"    the frontend cannot call it. Export it (capitalize the name) so Wails binds it, or if it\n"+
			"    is dead code, remove it.",
		m.File, m.Line, m.Name,
	)
}

// AppMethodDecl is the subset of an *ast.FuncDecl the checker needs: enough
// to identify a method on *App without carrying the whole erun-ui module's
// parsed AST through the package boundary.
type AppMethodDecl struct {
	Name      string
	Exported  bool
	File      string
	Line      int
	IdentUses int // occurrences of the bare identifier Name elsewhere in erun-ui's Go source, excluding this declaration
}

// FindUnboundAppMethods returns every unexported *App method with zero other
// references. decls must include every *App method declaration; identUses
// must already exclude the declaration's own identifier occurrence.
func FindUnboundAppMethods(decls []AppMethodDecl) []UnboundAppMethod {
	var unbound []UnboundAppMethod
	for _, d := range decls {
		if d.Exported {
			continue
		}
		if d.IdentUses > 0 {
			continue
		}
		unbound = append(unbound, UnboundAppMethod{Name: d.Name, File: d.File, Line: d.Line})
	}
	sort.Slice(unbound, func(i, j int) bool {
		if unbound[i].File != unbound[j].File {
			return unbound[i].File < unbound[j].File
		}
		return unbound[i].Line < unbound[j].Line
	})
	return unbound
}

// IsAppPointerReceiverMethod reports whether decl is a method on *App (not
// App by value, and not a free function), which is the receiver shape Wails
// binds.
func IsAppPointerReceiverMethod(decl *ast.FuncDecl) bool {
	if decl.Recv == nil || len(decl.Recv.List) != 1 {
		return false
	}
	star, ok := decl.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	ident, ok := star.X.(*ast.Ident)
	return ok && ident.Name == "App"
}

// IsExportedName reports whether a Go identifier is exported (capitalized).
func IsExportedName(name string) bool {
	return name != "" && ast.IsExported(name)
}
