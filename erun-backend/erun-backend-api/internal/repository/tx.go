package repository

import (
	"context"
	"database/sql"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/security"
)

type Dialect string

const (
	DialectSQLite     Dialect = "sqlite"
	DialectPostgres   Dialect = "postgres"
	DialectClickHouse Dialect = "clickhouse"
)

type TxManager struct {
	db      *sql.DB
	dialect Dialect
}

func NewTxManager(db *sql.DB, dialect Dialect) *TxManager {
	if dialect == "" {
		dialect = DialectSQLite
	}
	return &TxManager{db: db, dialect: dialect}
}

func (m *TxManager) WithinTx(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if m.dialect == DialectPostgres {
		securityContext, err := security.RequiredFromContext(ctx)
		if err != nil {
			return ErrMissingSecurityContext
		}
		if err := m.setPostgresSecurityContext(ctx, tx, securityContext); err != nil {
			return err
		}
	}

	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (m *TxManager) setPostgresSecurityContext(ctx context.Context, tx *sql.Tx, securityContext security.Context) error {
	role := "erun_tenant"
	if securityContext.TenantType == "OPERATIONS" {
		role = "erun_operations"
	}
	if _, err := tx.ExecContext(ctx, `SET LOCAL ROLE `+role); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('erun.tenant_id', $1, true)`, securityContext.TenantID); err != nil {
		return err
	}
	if strings.TrimSpace(securityContext.ErunUserID) == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('erun.user_id', $1, true)`, securityContext.ErunUserID); err != nil {
		return err
	}
	return nil
}

func (m *TxManager) rebind(query string) string {
	if m.dialect != DialectPostgres {
		return query
	}
	var b strings.Builder
	arg := 1
	for _, r := range query {
		if r == '?' {
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(arg))
			arg++
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
