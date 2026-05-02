package repository

import (
	"context"
	"database/sql"
	"regexp"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type PermissionAuthorizer struct {
	txs *TxManager
}

func NewPermissionAuthorizer(db *sql.DB) *PermissionAuthorizer {
	return NewPermissionAuthorizerForDialect(db, DialectSQLite)
}

func NewPermissionAuthorizerForDialect(db *sql.DB, dialect Dialect) *PermissionAuthorizer {
	return &PermissionAuthorizer{txs: NewTxManager(db, dialect)}
}

func (a *PermissionAuthorizer) Authorize(ctx context.Context, method string, apiPath string) error {
	securityContext, err := security.RequiredFromContext(ctx)
	if err != nil {
		return ErrMissingSecurityContext
	}

	err = a.txs.WithinTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, a.txs.rebind(`
			SELECT rp.api_method,
			       rp.api_path,
			       rp.api_method_pattern,
			       rp.api_path_pattern
			  FROM user_roles ur
			  JOIN role_permissions rp
			    ON rp.tenant_id = ur.tenant_id
			   AND rp.role_id = ur.role_id
			 WHERE ur.tenant_id = ?
			   AND ur.user_id = ?
		`), securityContext.TenantID, securityContext.ErunUserID)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var rule permissionRule
			if err := rows.Scan(
				&rule.APIMethod,
				&rule.APIPath,
				&rule.APIMethodPattern,
				&rule.APIPathPattern,
			); err != nil {
				return err
			}
			matches, err := rule.matches(method, apiPath)
			if err != nil {
				return err
			}
			if matches {
				return nil
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return ErrForbidden
	})
	return err
}

type permissionRule struct {
	APIMethod        sql.NullString
	APIPath          sql.NullString
	APIMethodPattern sql.NullString
	APIPathPattern   sql.NullString
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
