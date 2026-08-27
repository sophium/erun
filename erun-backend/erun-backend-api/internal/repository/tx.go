package repository

import (
	"context"
	"database/sql"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

type Dialect string

const (
	DialectPostgres Dialect = "postgres"
)

type TxManager struct {
	db      *bun.DB
	dialect Dialect
}

func NewTxManager(db *sql.DB, dialect Dialect) *TxManager {
	if dialect == "" {
		dialect = DialectPostgres
	}
	return &TxManager{db: bun.NewDB(db, pgdialect.New()), dialect: dialect}
}

func (m *TxManager) WithinTx(ctx context.Context, fn func(context.Context, bun.Tx) error) error {
	if m.dialect == DialectPostgres {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		return m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
			if err := m.setPostgresSecurityContext(ctx, tx, securityContext); err != nil {
				return err
			}
			return fn(ctx, tx)
		})
	}
	return m.db.RunInTx(ctx, nil, fn)
}

// WithinSystemTx runs fn in a transaction bound to the erun_operations
// database role with no caller identity and no tenant scoping — the narrow
// escape hatch AGENTS.md's Row-Level Security section calls for "before
// tenant context exists". Its one legitimate caller today is the invite
// accept flow: resolving an unauthenticated invite token to its target
// tenant has no security.Context to read a tenant from, the same problem
// root resolution tables like tenant_issuers solve by carrying no RLS at
// all, except invites is tenant-owned end to end (created, listed, and
// revoked under normal per-tenant RLS) so it cannot go that route. Every
// statement fn issues must name its own tenant_id explicitly:
// erun_operations' RLS policy grants it every tenant's rows regardless of
// erun.tenant_id, so nothing here provides tenant isolation on its own.
func (m *TxManager) WithinSystemTx(ctx context.Context, fn func(context.Context, bun.Tx) error) error {
	if m.dialect != DialectPostgres {
		return m.db.RunInTx(ctx, nil, fn)
	}
	return m.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE erun_operations`); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
}

func (m *TxManager) setPostgresSecurityContext(ctx context.Context, tx bun.Tx, securityContext security.Context) error {
	role := "erun_tenant"
	if securityContext.TenantType == "OPERATIONS" {
		role = "erun_operations"
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE `+role); err != nil {
		return err
	}
	if _, err := tx.NewRaw(`SELECT set_config('erun.tenant_id', ?, true)`, securityContext.TenantID).Exec(ctx); err != nil {
		return err
	}
	if strings.TrimSpace(securityContext.ErunUserID) == "" {
		return nil
	}
	if _, err := tx.NewRaw(`SELECT set_config('erun.user_id', ?, true)`, securityContext.ErunUserID).Exec(ctx); err != nil {
		return err
	}
	return nil
}
