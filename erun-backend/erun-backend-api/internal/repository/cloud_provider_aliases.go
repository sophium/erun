package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/secrets"
	"github.com/uptrace/bun"
)

// CloudProviderAliasRepository stores a tenant's BYO-cloud credentials that the
// provisioning executor resolves to reach the tenant's cloud; they are encrypted at rest.
type CloudProviderAliasRepository struct {
	txs    *TxManager
	cipher *secrets.Cipher
}

func NewCloudProviderAliasRepository(txs *TxManager, cipher *secrets.Cipher) *CloudProviderAliasRepository {
	return &CloudProviderAliasRepository{txs: txs, cipher: cipher}
}

// ResolvedCloudProviderAlias is a decrypted alias holding the plaintext credentials the provisioning executor uses to reach the tenant's cloud.
type ResolvedCloudProviderAlias struct {
	Alias       string
	Provider    string
	Credentials string
}

// Set upserts the tenant's alias. tenant_id is supplied by the RLS default, so it is omitted from the write.
func (r *CloudProviderAliasRepository) Set(ctx context.Context, alias, provider, credentials string) error {
	encrypted, err := r.cipher.Encrypt([]byte(credentials))
	if err != nil {
		return err
	}
	return r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		_, execErr := tx.NewRaw(`
			INSERT INTO cloud_provider_aliases (alias, provider, credentials_encrypted)
			VALUES (?, ?, ?)
			ON CONFLICT (tenant_id, alias) DO UPDATE
			   SET provider = EXCLUDED.provider,
			       credentials_encrypted = EXCLUDED.credentials_encrypted
		`, alias, provider, encrypted).Exec(ctx)
		return execErr
	})
}

// Get resolves a tenant's alias, returning ErrNotFound for an unknown alias. RLS scopes the read to the caller's tenant.
func (r *CloudProviderAliasRepository) Get(ctx context.Context, alias string) (ResolvedCloudProviderAlias, error) {
	var row struct {
		Provider             string `bun:"provider"`
		CredentialsEncrypted []byte `bun:"credentials_encrypted"`
	}
	err := r.txs.WithinTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		scanErr := tx.NewRaw(`
			SELECT provider, credentials_encrypted
			  FROM cloud_provider_aliases
			 WHERE alias = ?
		`, alias).Scan(ctx, &row)
		return normalizeNoRows(scanErr)
	})
	if err != nil {
		return ResolvedCloudProviderAlias{}, err
	}
	credentials, err := r.cipher.Decrypt(row.CredentialsEncrypted)
	if err != nil {
		return ResolvedCloudProviderAlias{}, err
	}
	return ResolvedCloudProviderAlias{Alias: alias, Provider: row.Provider, Credentials: string(credentials)}, nil
}
