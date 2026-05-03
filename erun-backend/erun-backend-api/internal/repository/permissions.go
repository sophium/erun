package repository

import (
	"context"
	"database/sql"
	"regexp"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
)

type PermissionAuthorizer struct {
	txs *TxManager
}

func NewPermissionAuthorizer(db *sql.DB) *PermissionAuthorizer {
	return NewPermissionAuthorizerForDialect(db, DialectPostgres)
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
		`, securityContext.ErunUserID).Scan(ctx, &rules); err != nil {
			return err
		}

		for _, rule := range rules {
			matches, err := rule.matches(method, apiPath)
			if err != nil {
				return err
			}
			if matches {
				return nil
			}
		}
		return ErrForbidden
	})
	return err
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
