// Package desktopsurface implements the classifier behind
// TestDesktopSurfaceGate: it flags a user-facing CLI command, MCP tool, API
// route, or operator-settable config field that has no way in from an
// operator surface -- erun-ui/frontend or erun-console (erun AGENTS.md §
// "Smooth, Seamless, No Dead Ends", failure mode 3) -- and separately flags a
// Wails binding location with no binding -- an unexported *App method no
// other Go code calls.
package desktopsurface

import (
	"fmt"
	"go/ast"
	"regexp"
	"sort"
	"strings"
)

// Capability is one user-reachable action the CLI, MCP, or API surface
// exposes.
type Capability struct {
	// Name identifies the capability for reporting: an MCP tool name, a CLI
	// command path joined with spaces (e.g. "app restart"), or an API route
	// as "METHOD /path" (e.g. "GET /v1/roles").
	Name string
	// Source names where the capability comes from, for the failure message.
	Source string
	// Token is searched for, case-insensitively, as a plain substring of the
	// operator surface source. It is deliberately not word-bounded: a
	// capability name embedded in a camelCase identifier (WhipButton,
	// renderWhipPanel) must still count as a match. Ignored when Pattern is
	// set.
	Token string
	// Tokens is Token's any-of form: the capability counts as referenced when
	// the operator surface contains any one of them. CLI flags use it because
	// one flag has two equally plausible spellings on the frontend side --
	// the kebab-case flag name a call site passes through verbatim
	// ("waiting-on-me") and the camelCase field a request model names it by
	// ("waitingOnMe") -- and either is a real way in. Ignored when Pattern is
	// set; Token is ignored when Tokens is non-empty.
	Tokens []string
	// Pattern, when set, is a regular expression searched for
	// case-insensitively instead of Token. API routes use this: a
	// parameterized path's literal segments (e.g. "/v1/users/{user_id}/roles")
	// never appear contiguously in frontend source once a call site
	// interpolates the id, and a route's own last segment alone ("roles") is
	// often an ordinary word that already shows up in unrelated UI copy --
	// see APIRoutePattern.
	Pattern string
	// AgentFacing marks a capability declared exempt from needing an
	// operator entry point. See eruncommon.MCPToolDescriptor.AgentFacing,
	// erun-cli/cmd.cliOnlyAgentFacingCommands, and
	// erun-backend-api/internal/routes.InternalAPIRoutes for the three
	// declaration sites.
	AgentFacing bool
	// KnownGap marks a capability recorded in a baseline of pre-existing gaps
	// (erun-backend-api/internal/routes.KnownUnsurfacedRoutes today) rather
	// than one that legitimately needs no surface. It is skipped by
	// FindMissingDesktopSurface exactly like AgentFacing, but
	// FindStaleBaselineEntries flags it the moment it gains a real reference,
	// so the baseline can only shrink and never quietly grows stale.
	KnownGap bool
	// WailsBinding is a second, independent way an API route capability can
	// be found referenced: the name of an exported erun-ui *App method whose
	// call graph is hand-verified to invoke this exact route. It exists
	// because a route reached only through the desktop never puts its literal
	// path in TypeScript -- erun-ui/frontend/src calls the Wails-bound Go
	// method by name, and only erun-ui/*.go (calling erun-common's
	// PlatformClient) holds the path -- so Pattern alone can never see it.
	// Checked as a plain substring against the same frontend source Pattern
	// and Token already read, but kept as its own field rather than folded
	// into Token so it cannot loosen an API route's existing Pattern-over-
	// Token precedent (see
	// TestFindMissingDesktopSurfaceUsesPatternOverTokenWhenBothCouldMatch):
	// Token alone is deliberately a loose, unverified guess, while a
	// WailsBinding entry is hand-verified true on both ends before it is set.
	WailsBinding string
	// DeclarationHint names, in prose, where a capability from this Source
	// declares itself exempt -- used verbatim in Missing.Message() so the
	// failure points at the fix instead of a generic pointer.
	DeclarationHint string
	// BaselineHint names, in prose, where a capability from this Source is
	// recorded as a known gap -- used verbatim in StaleBaselineEntry.Message()
	// so a capability that gained a surface points at removing its own
	// baseline entry instead of the unrelated DeclarationHint fix.
	BaselineHint string
}

// Missing is a capability the gate could not find any operator entry point
// for.
type Missing struct {
	Capability Capability
}

// Message names the gap and where to close it, so the gate's failure is
// itself a next action rather than a dead end (erun AGENTS.md § "Smooth,
// Seamless, No Dead Ends": "the check must name what is missing and where to
// add it, not merely fail").
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

// FrontendSource is the concatenated text of every operator-surface source
// file the gate searches: the desktop app (erun-ui/frontend/src) and the
// hosted web console (erun-console/src). Callers build this from those trees
// so the classifier itself stays free of filesystem access and is easy to
// unit test.
type FrontendSource string

// Contains reports whether the source references a token anywhere,
// case-insensitively.
func (s FrontendSource) Contains(token string) bool {
	return s.prepare().contains(token)
}

// preparedSource is a FrontendSource with its lowercased form computed once.
// The two operator-surface trees concatenate to megabytes, and the gate now
// audits hundreds of capabilities against them, so lowercasing per capability
// would re-walk the whole source for every check.
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

func (p preparedSource) containsPattern(pattern string) bool {
	return regexp.MustCompile("(?i)" + pattern).MatchString(p.raw)
}

// ContainsPattern reports whether the source matches the regular expression
// pattern anywhere, case-insensitively.
func (s FrontendSource) ContainsPattern(pattern string) bool {
	return regexp.MustCompile("(?i)" + pattern).MatchString(string(s))
}

// APIRoutePattern builds the search pattern for an API route's canonical
// path template (e.g. "/v1/users/{user_id}/roles", as registered with
// ProtectedRouteRegistrar). Every literal segment must appear, in order,
// exactly as written; each "{param}" segment matches whatever a frontend
// call site interpolates in its place -- a template expression, string
// concatenation, an encodeURIComponent(...) call -- bounded to that one path
// segment (no "/", quote, or backtick) so the match cannot wander into
// unrelated code.
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

// FindMissingDesktopSurface returns every non-agent-facing capability with no
// reference in frontendSource, ordered by name for a stable report.
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

// referencedInFrontend reports whether c's Pattern (or, absent one, its
// Tokens/Token) appears in frontendSource. A Pattern-bearing capability with
// no Pattern match gets one more chance through WailsBinding, since some
// routes are only ever reachable from TypeScript by a Go method name, never
// their own path -- see Capability.WailsBinding.
func referencedInFrontend(c Capability, frontendSource preparedSource) bool {
	if c.Pattern != "" {
		if frontendSource.containsPattern(c.Pattern) {
			return true
		}
		return c.WailsBinding != "" && frontendSource.contains(c.WailsBinding)
	}
	if len(c.Tokens) > 0 {
		for _, token := range c.Tokens {
			if frontendSource.contains(token) {
				return true
			}
		}
		return false
	}
	return frontendSource.contains(c.Token)
}

// StaleBaselineEntry is a KnownGap capability that has gained a real
// operator-surface reference since it was baselined, so it must be removed
// from the baseline rather than left to amnesty a gap that no longer exists.
type StaleBaselineEntry struct {
	Capability Capability
}

// Message names the capability and where to remove its now-stale baseline
// entry, distinct from Missing.Message()'s "add a surface or declare
// internal" -- this capability already has a surface, so the fix is the
// opposite: delete the entry that once excused it.
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

// FindStaleBaselineEntries returns every KnownGap capability that now has a
// real reference in frontendSource, ordered by name for a stable report. A
// baseline that never sheds entries it has outgrown rots into a permanent
// amnesty, so this is what lets the gate enforce "the baseline only shrinks"
// rather than merely documenting it.
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

// UnboundAppMethod is a method on *App that Wails would silently skip when
// generating bindings: unexported, so the binding generator never sees it,
// and referenced by no other Go code in erun-ui, so nothing calls it either.
// That combination is the whip shape: the author placed the method where a
// binding belongs, got no binding, and got no error.
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
