package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/uptrace/bun"
)

const usageEventColumns = `usage_event_id, tenant_id, environment_id, event_type, cpu_millicores, memory_mb, storage_gb, created_at`

type UsageEventRepository struct {
	txs *TxManager
}

func NewUsageEventRepository(txs *TxManager) *UsageEventRepository {
	return &UsageEventRepository{txs: txs}
}

// Record inserts one metering event for the caller's tenant (RLS scopes the
// insert; tenant_id defaults from erun_current_tenant_id() and is never taken
// from the caller-supplied value).
func (r *UsageEventRepository) Record(ctx context.Context, event model.UsageEvent) error {
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewRaw(`
			INSERT INTO usage_events (environment_id, event_type, cpu_millicores, memory_mb, storage_gb)
			VALUES (?, ?, ?, ?, ?)
		`,
			nullString(event.EnvironmentID),
			event.EventType,
			nullZero(event.CPUMillicores),
			nullZero(event.MemoryMB),
			nullZero(event.StorageGB),
		).Exec(ctx)
		return err
	})
}

// List returns the caller's tenant's usage events, most recent first.
func (r *UsageEventRepository) List(ctx context.Context) ([]model.UsageEvent, error) {
	var events []model.UsageEvent
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		return tx.NewRaw(`
			SELECT `+usageEventColumns+`
			  FROM usage_events
			 ORDER BY created_at DESC, usage_event_id DESC
		`).Scan(ctx, &events)
	})
	return events, err
}

func nullZero(value int) any {
	if value == 0 {
		return nil
	}
	return value
}
