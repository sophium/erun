package repository

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A repository method whose only parameter is a context.Context has no
// caller-supplied argument that could identify a single row or narrow a
// query — its result set can only be scoped to a tenant by something the
// method reads out of the security context, or left to PostgreSQL RLS.
// erun_operations' RLS policy is unconditional (USING (true), by design, so
// an OPERATIONS caller's cross-tenant reads keep working), so a method of
// this shape that never reads the security context returns every tenant's
// rows for an OPERATIONS caller instead of its own — the defect that made
// EnvironmentRepository.Count/List and TenantQuotaRepository.Get answer a
// quota question with the whole platform's total.
//
// A second, structurally different group has the same defect: a method that
// takes a filter struct (or a plain scalar such as ListMergeQueue's
// targetBranch) instead of bare ctx. Its caller-supplied argument narrows the
// query but says nothing about the tenant, so an empty/wide filter has
// exactly the context-only methods' problem — every tenant's rows for an
// OPERATIONS caller. That shape can't be told apart from a legitimate single-
// row `Get(ctx, id)` lookup by parameter count alone, so the scan below keys
// on the "List" naming convention this module's AGENTS.md already
// establishes for collection-returning methods instead: every top-level
// method named `List` or `List*` is in scope, regardless of its parameter
// list.
//
// tenantScopeClassification is every method of either shape found in this
// package. TestContextOnlyRepositoryMethodsAreClassified fails when a new
// one appears with no entry, forcing whoever adds it to consciously pick a
// kind below instead of silently repeating the mistake, and fails when an
// entry's method no longer exists, so a rename cannot leave a stale label
// behind. It does not by itself prove a scopedExplicitly entry is correct —
// that is what the OPERATIONS-caller regression tests in
// environment_delete_e2e_test.go, tenant_quotas_e2e_test.go, and
// operations_scope_e2e_test.go are for.
type tenantScopeKind int

const (
	// scopedExplicitly means the method reads TenantID off the security
	// context and applies it as an explicit query predicate rather than
	// relying on RLS alone.
	scopedExplicitly tenantScopeKind = iota
	// deliberatelyCrossTenant means every tenant's rows (or rows keyed by
	// the caller's identity rather than its resolved tenant) are the
	// intended answer, documented as such where the method is declared.
	deliberatelyCrossTenant
	// notTenantOwned means the table behind the method carries no row-level
	// security at all, so erun_operations' unconditional policy is not the
	// mechanism in play here.
	notTenantOwned
	// trackedDebt means the method has the same shape as the defect above
	// and has not been fixed yet.
	trackedDebt
)

// String names a kind for the classification prompt below, so the message
// naming valid choices can't drift out of sync with the identifiers a fix
// actually assigns.
func (k tenantScopeKind) String() string {
	switch k {
	case scopedExplicitly:
		return "scopedExplicitly"
	case deliberatelyCrossTenant:
		return "deliberatelyCrossTenant"
	case notTenantOwned:
		return "notTenantOwned"
	case trackedDebt:
		return "trackedDebt"
	default:
		return "unknown"
	}
}

type tenantScopeEntry struct {
	kind   tenantScopeKind
	reason string
}

var tenantScopeClassification = map[string]tenantScopeEntry{
	"EnvironmentRepository.List": {
		kind:   scopedExplicitly,
		reason: "reads TenantID off the security context and filters WHERE tenant_id = ?",
	},
	"EnvironmentRepository.Count": {
		kind:   scopedExplicitly,
		reason: "reads TenantID off the security context and filters both subqueries WHERE tenant_id = ?",
	},
	"TenantQuotaRepository.Get": {
		kind:   scopedExplicitly,
		reason: "reads TenantID off the security context and filters WHERE tenant_id = ?",
	},
	"TenantQuotaRepository.MaxEnvironments": {
		kind:   scopedExplicitly,
		reason: "delegates entirely to the already-scoped TenantQuotaRepository.Get",
	},
	"TenantRepository.Current": {
		kind:   scopedExplicitly,
		reason: "tenants carries no RLS at all, so it filters WHERE tenant_id = ? against the security context's TenantID itself",
	},
	"TenantRepository.List": {
		kind:   deliberatelyCrossTenant,
		reason: "a non-operations caller's answer comes from the tenant_id-scoped Current; an operations caller deliberately sees every tenant, the tenant-management capability this method exists for",
	},
	"TenantRepository.Reachable": {
		kind:   deliberatelyCrossTenant,
		reason: "answers 'which tenants does the caller's own verified identity map to' by (issuer, external_id) across every tenant_id, never by the caller's own resolved tenant — documented at its declaration",
	},
	"PlatformRateLimitRepository.Get": {
		kind:   notTenantOwned,
		reason: "platform_rate_limits carries no row-level security; it is one row of global platform configuration, not a tenant-owned table",
	},
	"UsageEventRepository.List": {
		kind:   scopedExplicitly,
		reason: "reads TenantID off the security context and filters WHERE tenant_id = ?",
	},
	"ContextRepository.List": {
		kind:   scopedExplicitly,
		reason: "reads TenantID off the security context and filters WHERE tenant_id = ?",
	},
	"RoleRepository.List": {
		kind:   scopedExplicitly,
		reason: "reads TenantID off the security context and filters both the roles and role_permissions SELECTs WHERE tenant_id = ?, in addition to using it to bootstrap predefined roles",
	},
	"EnvironmentRepository.ListByStatuses": {
		kind:   deliberatelyCrossTenant,
		reason: "the delete reconciler's own re-attempt sweep is documented at its declaration as intentionally cross-tenant: it must see every tenant's mid-teardown rows to re-attempt them",
	},
	"UserRepository.List": {
		kind:   scopedExplicitly,
		reason: "filter.TenantID is always resolved by the route (the caller's own tenant by default, an explicit override only for an operations-scoped caller) before this filters WHERE tenant_id = ?",
	},
	"InviteRepository.List": {
		kind:   scopedExplicitly,
		reason: "filter.TenantID is always resolved by the route the same way UserFilter's is, before this filters WHERE tenant_id = ?",
	},
	"TenantIssuerRepository.List": {
		kind:   scopedExplicitly,
		reason: "filter.TenantID is always resolved by the route the same way UserFilter's is, before this filters WHERE tenant_id = ?",
	},
	"InviteRequestRepository.List": {
		kind:   notTenantOwned,
		reason: "invite_requests carries no tenant_id column or RLS at all (documented at the repository's own declaration) — its submitter has no tenant yet, so there is no tenant boundary for erun_operations to bypass",
	},
	"AuditEventRepository.List": {
		kind:   scopedExplicitly,
		reason: "reads TenantID off the security context and filters WHERE tenant_id = ?",
	},
	"BuildRepository.List": {
		kind:   scopedExplicitly,
		reason: "reads TenantID off the security context and filters WHERE b.tenant_id = ?",
	},
	"CommentRepository.List": {
		kind:   scopedExplicitly,
		reason: "reads TenantID off the security context and filters WHERE tenant_id = ?",
	},
	"ReviewRepository.List": {
		kind:   scopedExplicitly,
		reason: "reads TenantID off the security context and filters WHERE r.tenant_id = ?",
	},
	"ReviewRepository.ListMergeQueue": {
		kind:   scopedExplicitly,
		reason: "reads TenantID off the security context and filters WHERE q.tenant_id = ?",
	},
	"ReviewReviewerRepository.List": {
		kind:   scopedExplicitly,
		reason: "reads TenantID off the security context and filters WHERE tenant_id = ?",
	},
	"ReleaseRepository.List": {
		kind:   scopedExplicitly,
		reason: "reads TenantID off the security context and filters WHERE tenant_id = ?",
	},
	"AISessionRepository.List": {
		kind:   scopedExplicitly,
		reason: "reads TenantID off the security context and filters WHERE tenant_id = ? alongside the caller-supplied environment_id",
	},
}

// TestContextOnlyRepositoryMethodsAreClassified is the structural half of the
// contract described above. It does not evaluate SQL text: a query can
// mention tenant_id in a JOIN condition without that scoping the result to
// the caller, so the only reliable structural signal is "did this method
// even look at who is calling" — everything else is a judgment call the
// classification map above records instead of a heuristic that could be
// walked through.
func TestContextOnlyRepositoryMethodsAreClassified(t *testing.T) {
	root, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	found := tenantScopeSensitiveRepositoryMethods(t, root)
	if len(found) == 0 {
		t.Fatal("found no tenant-scope-sensitive repository methods — the scan is misconfigured")
	}

	for _, name := range found {
		if _, ok := tenantScopeClassification[name]; !ok {
			t.Errorf("%s takes only (ctx context.Context), or is a List-shaped method, with no entry in "+
				"tenantScopeClassification — classify it as %s, %s, %s, or %s and say why",
				name, scopedExplicitly, deliberatelyCrossTenant, notTenantOwned, trackedDebt)
		}
	}

	foundSet := make(map[string]bool, len(found))
	for _, name := range found {
		foundSet[name] = true
	}
	var stale []string
	for name := range tenantScopeClassification {
		if !foundSet[name] {
			stale = append(stale, name)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("tenantScopeClassification names methods that no longer exist (renamed or removed): %v", stale)
	}
}

// tenantScopeSensitiveRepositoryMethods parses every non-test .go file in
// this package's directory and returns "ReceiverType.MethodName" for every
// method matching either shape the classification map covers: context-only,
// or named List/List*.
func tenantScopeSensitiveRepositoryMethods(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	seen := make(map[string]bool)
	var methods []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		for _, found := range append(contextOnlyMethodsInFile(file), listShapedMethodsInFile(file)...) {
			if !seen[found] {
				seen[found] = true
				methods = append(methods, found)
			}
		}
	}
	sort.Strings(methods)
	return methods
}

// listShapedMethodsInFile returns "ReceiverType.MethodName" for every
// top-level method declaration in file whose name is List or starts with
// List, regardless of its parameter list — the naming convention this
// module's AGENTS.md establishes for methods that return a collection.
func listShapedMethodsInFile(file *ast.File) []string {
	var methods []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil {
			continue
		}
		receiver := receiverTypeName(fn.Recv)
		if receiver == "" || !strings.HasPrefix(fn.Name.Name, "List") {
			continue
		}
		methods = append(methods, receiver+"."+fn.Name.Name)
	}
	return methods
}

// contextOnlyMethodsInFile returns "ReceiverType.MethodName" for every
// top-level method declaration in file whose parameter list is exactly one
// context.Context.
func contextOnlyMethodsInFile(file *ast.File) []string {
	var methods []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil {
			continue
		}
		receiver := receiverTypeName(fn.Recv)
		if receiver == "" || !isContextOnlySignature(fn.Type) {
			continue
		}
		methods = append(methods, receiver+"."+fn.Name.Name)
	}
	return methods
}

// receiverTypeName returns the pointer receiver's named type, or "" for a
// value receiver or any other shape this package does not use.
func receiverTypeName(recv *ast.FieldList) string {
	if len(recv.List) != 1 {
		return ""
	}
	star, ok := recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return ""
	}
	ident, ok := star.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return ident.Name
}

// isContextOnlySignature reports whether fn's parameter list is exactly one
// parameter of type context.Context.
func isContextOnlySignature(fn *ast.FuncType) bool {
	if fn.Params == nil || len(fn.Params.List) != 1 {
		return false
	}
	field := fn.Params.List[0]
	if len(field.Names) != 1 {
		return false
	}
	selector, ok := field.Type.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := selector.X.(*ast.Ident)
	return ok && pkg.Name == "context" && selector.Sel.Name == "Context"
}
