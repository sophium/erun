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

// A context-only or List* repository method can scope to a tenant only via the
// security context, since erun_operations' RLS policy is unconditional. Every
// such method must be classified so new ones are a conscious choice.
type tenantScopeKind int

const (
	// scopedExplicitly applies the security context's TenantID as a query predicate.
	scopedExplicitly tenantScopeKind = iota
	// deliberatelyCrossTenant returns every tenant's rows by design, documented at the method.
	deliberatelyCrossTenant
	// notTenantOwned reads a table with no RLS.
	notTenantOwned
	// trackedDebt means the method has the defect and is not fixed yet.
	trackedDebt
)

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
	"BuildRepository.ListPage": {
		kind:   scopedExplicitly,
		reason: "reads TenantID off the security context and filters WHERE b.tenant_id = ?, same as List",
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
	"JobRepository.List": {
		kind:   scopedExplicitly,
		reason: "reads TenantID off the security context and filters WHERE tenant_id = ? before any caller-supplied filter narrows it further",
	},
	"JobRepository.ListByEnvironment": {
		kind:   scopedExplicitly,
		reason: "delegates to the already-scoped list, which adds the caller-supplied environment_id on top of WHERE tenant_id = ?",
	},
}

// Structural only: SQL mentioning tenant_id need not scope to the caller.
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

// tenantScopeSensitiveRepositoryMethods returns context-only and List* methods
// of this package's non-test files.
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
