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
// tenantScopeClassification is every method of that shape found in this
// package. TestContextOnlyRepositoryMethodsAreClassified fails when a new
// one appears with no entry, forcing whoever adds it to consciously pick a
// kind below instead of silently repeating the mistake, and fails when an
// entry's method no longer exists, so a rename cannot leave a stale label
// behind. It does not by itself prove a scopedExplicitly entry is correct —
// that is what the OPERATIONS-caller regression tests in
// environment_delete_e2e_test.go and tenant_quotas_e2e_test.go are for.
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
		kind:   trackedDebt,
		reason: "unfiltered SELECT over usage_events with no tenant predicate; an operations caller sees every tenant's metering events",
	},
	"ContextRepository.List": {
		kind:   trackedDebt,
		reason: "unfiltered SELECT over contexts with no tenant predicate; an operations caller sees every tenant's cloud contexts",
	},
	"RoleRepository.List": {
		kind:   trackedDebt,
		reason: "reads the security context only to bootstrap predefined roles, never to filter the roles/role_permissions SELECTs; an operations caller sees every tenant's roles",
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
	found := contextOnlyRepositoryMethods(t, root)
	if len(found) == 0 {
		t.Fatal("found no context-only repository methods — the scan is misconfigured")
	}

	for _, name := range found {
		if _, ok := tenantScopeClassification[name]; !ok {
			t.Errorf("%s takes only (ctx context.Context) with no entry in tenantScopeClassification — "+
				"classify it as scopedExplicitly, deliberatelyCrossTenant, notTenantOwned, or trackedDebt "+
				"and say why", name)
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

// contextOnlyRepositoryMethods parses every non-test .go file in this
// package's directory and returns "ReceiverType.MethodName" for every method
// whose parameter list is exactly one context.Context.
func contextOnlyRepositoryMethods(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
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
		methods = append(methods, contextOnlyMethodsInFile(file)...)
	}
	sort.Strings(methods)
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
