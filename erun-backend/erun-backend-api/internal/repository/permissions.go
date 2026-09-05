package repository

import (
	"context"
	"database/sql"
	"regexp"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/uptrace/bun"
)

type PermissionAuthorizer struct {
	txs *TxManager
}

func NewPermissionAuthorizerForDialect(db *sql.DB, dialect Dialect) *PermissionAuthorizer {
	return &PermissionAuthorizer{txs: NewTxManager(db, dialect)}
}

func (a *PermissionAuthorizer) Authorize(ctx context.Context, method string, apiPath string) error {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return ErrMissingSecurityContext
	}

	err = a.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		rules, err := userPermissionRules(ctx, tx, securityContext.ErunUserID)
		if err != nil {
			return err
		}
		allowed, err := rulesAllow(rules, method, apiPath)
		if err != nil {
			return err
		}
		if allowed {
			return nil
		}
		return ErrForbidden
	})
	return err
}

// PermittedRoutes narrows candidates to the ones this caller may reach. It
// reads the same rules through the same query as Authorize and decides each
// candidate with the same matcher, so what a client renders from and what the
// middleware enforces cannot drift.
func (a *PermissionAuthorizer) PermittedRoutes(ctx context.Context, candidates []eruncommon.PlatformCapability) ([]eruncommon.PlatformCapability, error) {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return nil, ErrMissingSecurityContext
	}

	var permitted []eruncommon.PlatformCapability
	err = a.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		rules, err := userPermissionRules(ctx, tx, securityContext.ErunUserID)
		if err != nil {
			return err
		}
		permitted = nil
		for _, candidate := range candidates {
			allowed, err := rulesAllow(rules, candidate.Method, candidate.Path)
			if err != nil {
				return err
			}
			if allowed {
				permitted = append(permitted, candidate)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return permitted, nil
}

// userPermissionRules reads every permission rule the user's roles carry. It is
// the single query both the enforcement answer and the capability answer are
// derived from.
func userPermissionRules(ctx context.Context, tx bun.Tx, erunUserID string) ([]permissionRule, error) {
	var rules []permissionRule
	if err := tx.NewRaw(`
		SELECT rp.api_method,
		       rp.api_path,
		       rp.api_method_pattern,
		       rp.api_path_pattern
		  FROM user_roles ur
		  JOIN role_permissions rp
		    ON rp.tenant_id = ur.tenant_id
		   AND rp.role_id = ur.role_id
		 WHERE ur.user_id = ?
	`, erunUserID).Scan(ctx, &rules); err != nil {
		return nil, err
	}
	return rules, nil
}

func rulesAllow(rules []permissionRule, method string, apiPath string) (bool, error) {
	for _, rule := range rules {
		matches, err := rule.matches(method, apiPath)
		if err != nil {
			return false, err
		}
		if matches {
			return true, nil
		}
	}
	return false, nil
}

type permissionRule struct {
	APIMethod        sql.NullString `bun:"api_method"`
	APIPath          sql.NullString `bun:"api_path"`
	APIMethodPattern sql.NullString `bun:"api_method_pattern"`
	APIPathPattern   sql.NullString `bun:"api_path_pattern"`
}

func (r permissionRule) matches(method string, apiPath string) (bool, error) {
	if r.APIMethod.Valid && r.APIPath.Valid {
		return r.APIMethod.String == method && r.APIPath.String == apiPath, nil
	}
	if !r.APIMethodPattern.Valid || !r.APIPathPattern.Valid {
		return false, nil
	}
	methodMatches, err := regexp.MatchString(r.APIMethodPattern.String, method)
	if err != nil || !methodMatches {
		return false, err
	}
	pathMatches, err := regexp.MatchString(r.APIPathPattern.String, apiPath)
	if err != nil {
		return false, err
	}
	return pathMatches, nil
}
