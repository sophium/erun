package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/secrets"
	"github.com/uptrace/bun"
)

// ContextCredentialRepository custodies a context's k3s admin token, encrypted at
// rest and deliberately kept out of the contexts read model. One row per context.
type ContextCredentialRepository struct {
	txs    *TxManager
	cipher *secrets.Cipher
}

func NewContextCredentialRepository(txs *TxManager, cipher *secrets.Cipher) *ContextCredentialRepository {
	return &ContextCredentialRepository{txs: txs, cipher: cipher}
}

// Set upserts a context's k3s admin token; tenant_id is owned by the RLS default.
func (r *ContextCredentialRepository) Set(ctx context.Context, contextID, k3sAdminToken string) error {
	encrypted, err := r.cipher.Encrypt([]byte(k3sAdminToken))
	if err != nil {
		return err
	}
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, execErr := tx.NewRaw(`
			INSERT INTO context_credentials (context_id, k3s_admin_token_encrypted)
			VALUES (?, ?)
			ON CONFLICT (tenant_id, context_id) DO UPDATE
			   SET k3s_admin_token_encrypted = EXCLUDED.k3s_admin_token_encrypted
		`, contextID, encrypted).Exec(ctx)
		return execErr
	})
}

// Get returns the decrypted k3s admin token for a context, or ErrNotFound when the
// context has no custodied token.
func (r *ContextCredentialRepository) Get(ctx context.Context, contextID string) (string, error) {
	var encrypted []byte
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		scanErr := tx.NewRaw(`
			SELECT k3s_admin_token_encrypted
			  FROM context_credentials
			 WHERE context_id = ?
		`, contextID).Scan(ctx, &encrypted)
		return normalizeNoRows(scanErr)
	})
	if err != nil {
		return "", err
	}
	token, err := r.cipher.Decrypt(encrypted)
	if err != nil {
		return "", err
	}
	return string(token), nil
}
