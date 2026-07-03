package repository

import (
	"context"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/secrets"
	"github.com/uptrace/bun"
)

// CloudProviderAliasRepository stores a tenant's BYO-cloud credentials, the
// secret the provisioning executor resolves to talk to the tenant's cloud.
// Credentials are encrypted at rest; the bytea column never holds plaintext.
type CloudProviderAliasRepository struct {
	txs    *TxManager
	cipher *secrets.Cipher
}

func NewCloudProviderAliasRepository(txs *TxManager, cipher *secrets.Cipher) *CloudProviderAliasRepository {
	return &CloudProviderAliasRepository{txs: txs, cipher: cipher}
}

// ResolvedCloudProviderAlias is a decrypted alias: the provider plus the
// plaintext credentials blob the executor hands to the cloud SDK/CLI.
type ResolvedCloudProviderAlias struct {
	Alias       string
	Provider    string
	Credentials string
}

// Set upserts the tenant's alias, encrypting credentials before they touch the
// database. tenant_id is owned by the RLS default, so it is omitted.
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

// Get resolves a tenant's alias and decrypts its credentials. RLS scopes the
// read to the caller's tenant; an unknown alias returns ErrNotFound.
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
