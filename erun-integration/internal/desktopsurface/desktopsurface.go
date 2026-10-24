// Package desktopsurface flags user-facing capabilities with no operator surface entry point,
// and unexported *App methods Wails cannot bind.
package desktopsurface

import (
	"fmt"
	"go/ast"
	"regexp"
	"sort"
	"strings"
)

// Capability is one user-reachable action the CLI, MCP, or API surface exposes.
type Capability struct {
	// Name is an MCP tool name, a space-joined CLI command path, or "METHOD /path".
	Name   string
	Source string
	// Token is a case-insensitive substring match, deliberately not word-bounded so camelCase
	// identifiers still match. Ignored when Pattern is set.
	Token string
	// Tokens is Token's any-of form, for a CLI flag's kebab-case and camelCase spellings.
	Tokens []string
	// FlagTokens matches Tokens with containsFlagIdentifier, since a flag's camelCase spelling
	// is an ordinary word that would match unrelated field reads.
	FlagTokens bool
	// Pattern is a case-insensitive regexp used instead of Token; see APIRoutePattern.
	Pattern string
	// AgentFacing exempts the capability from needing an operator entry point.
	AgentFacing bool
	// KnownGap marks a baselined pre-existing gap; FindStaleBaselineEntries flags it once surfaced,
	// so the baseline only shrinks.
	KnownGap bool
	// WailsBinding is a hand-verified *App method name that invokes this route, for routes the
	// frontend reaches only through a Wails binding. Kept apart from Token so it cannot loosen Pattern.
	WailsBinding string
	// DeclarationHint names where this Source declares exemptions, for Missing.Message.
	DeclarationHint string
	// BaselineHint names where this Source records known gaps, for StaleBaselineEntry.Message.
	BaselineHint string
}

// Missing is a capability with no operator entry point.
type Missing struct {
	Capability Capability
}

// Message names the gap and where to close it.
func (m Missing) Message() string {
	hint := m.Capability.DeclarationHint
	if hint == "" {
		hint = "erun-common/mcp_tools.go's AgentFacing field for an MCP tool, " +
			"erun-cli/cmd/command_tree.go's cliOnlyAgentFacingCommands for a CLI-only command, " +
			"erun-backend-api/internal/routes/route_audit.go's InternalAPIRoutes for an API route, " +
			"or erun-common/operator_settable_config.go's OperatorSettableConfigFields for a config field"
	}
	what := fmt.Sprintf("%q", m.Capability.Token)
	switch {
	case m.Capability.Pattern != "":
		what = fmt.Sprintf("anything matching %q", m.Capability.Pattern)
	case len(m.Capability.Tokens) > 0:
		quoted := make([]string, 0, len(m.Capability.Tokens))
		for _, token := range m.Capability.Tokens {
			quoted = append(quoted, fmt.Sprintf("%q", token))
		}
		what = "any of " + strings.Join(quoted, " / ")
	}
	return fmt.Sprintf(
		"%s %q has no operator entry point: neither erun-ui/frontend/src nor erun-console/src reference %s.\n"+
			"    Either add a way to reach it from one of those trees, or if it is genuinely "+
			"agent-only or internal, declare that explicitly (%s).",
		m.Capability.Source, m.Capability.Name, what, hint,
	)
}

// FrontendSource is the concatenated operator-surface source (erun-ui/frontend/src and erun-console/src).
type FrontendSource string

// Contains reports whether the source references token, case-insensitively.
func (s FrontendSource) Contains(token string) bool {
	return s.prepare().contains(token)
}

// preparedSource caches the lowercased source, which is megabytes audited hundreds of times.
type preparedSource struct {
	raw   string
	lower string
}

func (s FrontendSource) prepare() preparedSource {
	return preparedSource{raw: string(s), lower: strings.ToLower(string(s))}
}

func (p preparedSource) contains(token string) bool {
	return strings.Contains(p.lower, strings.ToLower(token))
}

// containsFlagIdentifier ignores property accesses (`x.roleName`) and optional field declarations
// (`roleName?:`), neither of which can pass a flag.
func (p preparedSource) containsFlagIdentifier(token string) bool {
	needle := strings.ToLower(token)
	for from := 0; ; {
		i := strings.Index(p.lower[from:], needle)
		if i < 0 {
			return false
		}
		at := from + i
		after := at + len(needle)
		propertyAccess := at > 0 && p.lower[at-1] == '.'
		optionalField := strings.HasPrefix(p.lower[after:], "?:")
		if !propertyAccess && !optionalField {
			return true
		}
		from = after
	}
}

func (p preparedSource) containsPattern(pattern string) bool {
	return regexp.MustCompile("(?i)" + pattern).MatchString(p.raw)
}

// ContainsPattern reports whether the source matches pattern, case-insensitively.
func (s FrontendSource) ContainsPattern(pattern string) bool {
	return regexp.MustCompile("(?i)" + pattern).MatchString(string(s))
}

// APIRoutePattern matches a route's literal segments in order, with each {param} matching one
// interpolated path segment (no "/", quote, or backtick).
func APIRoutePattern(path string) string {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	parts := make([]string, 0, len(segments))
	for _, segment := range segments {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			parts = append(parts, "[^/`\"']*")
			continue
		}
		parts = append(parts, regexp.QuoteMeta(segment))
	}
	return "/" + strings.Join(parts, "/")
}

// FindMissingDesktopSurface returns every non-exempt capability with no reference, sorted by name.
func FindMissingDesktopSurface(capabilities []Capability, frontendSource FrontendSource) []Missing {
	prepared := frontendSource.prepare()
	var missing []Missing
	for _, c := range capabilities {
		if c.AgentFacing || c.KnownGap {
			continue
		}
		if referencedInFrontend(c, prepared) {
			continue
		}
		missing = append(missing, Missing{Capability: c})
	}
	sort.Slice(missing, func(i, j int) bool {
		return missing[i].Capability.Name < missing[j].Capability.Name
	})
	return missing
}

// referencedInFrontend falls back to WailsBinding when a Pattern does not match.
func referencedInFrontend(c Capability, frontendSource preparedSource) bool {
	if c.Pattern != "" {
		if frontendSource.containsPattern(c.Pattern) {
			return true
		}
		return c.WailsBinding != "" && frontendSource.contains(c.WailsBinding)
	}
	if len(c.Tokens) > 0 {
		for _, token := range c.Tokens {
			if c.FlagTokens {
				if frontendSource.containsFlagIdentifier(token) {
					return true
				}
				continue
			}
			if frontendSource.contains(token) {
				return true
			}
		}
		return false
	}
	return frontendSource.contains(c.Token)
}

// StaleBaselineEntry is a KnownGap capability that now has an operator-surface reference.
type StaleBaselineEntry struct {
	Capability Capability
}

// Message names the stale baseline entry to remove.
func (s StaleBaselineEntry) Message() string {
	hint := s.Capability.BaselineHint
	if hint == "" {
		hint = "its baseline entry in erun-backend-api/internal/routes/route_audit.go's KnownUnsurfacedRoutes map"
	}
	return fmt.Sprintf(
		"%s %q now has an operator entry point but is still listed as a known gap.\n"+
			"    Remove it from %s -- the baseline records gaps that still exist, and this one no longer does.",
		s.Capability.Source, s.Capability.Name, hint,
	)
}

// FindStaleBaselineEntries returns every KnownGap capability now referenced, sorted by name.
func FindStaleBaselineEntries(capabilities []Capability, frontendSource FrontendSource) []StaleBaselineEntry {
	prepared := frontendSource.prepare()
	var stale []StaleBaselineEntry
	for _, c := range capabilities {
		if !c.KnownGap {
			continue
		}
		if referencedInFrontend(c, prepared) {
			stale = append(stale, StaleBaselineEntry{Capability: c})
		}
	}
	sort.Slice(stale, func(i, j int) bool {
		return stale[i].Capability.Name < stale[j].Capability.Name
	})
	return stale
}

// UnboundAppMethod is an unexported *App method no other erun-ui Go code references, so Wails
// silently skips it.
type UnboundAppMethod struct {
	Name string
	File string
	Line int
}

func (m UnboundAppMethod) Message() string {
	return fmt.Sprintf(
		"%s:%d: (*App).%s is unexported and called from nowhere else in erun-ui.\n"+
			"    Wails only binds exported methods on *App, so this method has no generated binding and\n"+
			"    the frontend cannot call it. Export it (capitalize the name) so Wails binds it, or if it\n"+
			"    is dead code, remove it.",
		m.File, m.Line, m.Name,
	)
}

// AppMethodDecl is the subset of an *ast.FuncDecl the checker needs.
type AppMethodDecl struct {
	Name      string
	Exported  bool
	File      string
	Line      int
	IdentUses int // occurrences of the bare identifier Name elsewhere in erun-ui's Go source, excluding this declaration
}

// FindUnboundAppMethods returns every unexported *App method with zero other references;
// IdentUses must exclude the declaration itself.
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

// IsAppPointerReceiverMethod reports whether decl is a method on *App, the receiver shape Wails binds.
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

// IsExportedName reports whether name is exported.
func IsExportedName(name string) bool {
	return name != "" && ast.IsExported(name)
}
