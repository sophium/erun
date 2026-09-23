package backendapi

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/secrets"
)

// The operate path — the reads and writes a deploy, stop, delete, or job
// report drives — used to key every row on an id alone. That is only safe
// while RLS is the enforcement: contexts, context_credentials, environments,
// jobs, builds, and gate_runs all carry
// `FOR ALL TO erun_operations USING (true)`, so an OPERATIONS caller naming a
// stranger tenant's id was answered with that stranger's row by every one of
// them. These tests pin the property against a real migrated PostgreSQL and
// the real erun_operations policy, because that is the only venue in which the
// difference between "the SQL scopes it" and "RLS would have scoped it" is
// observable at all.

// operatePathCipher builds a real AES-256-GCM cipher, so the credential tests
// exercise the actual encrypt-then-custody-then-decrypt path rather than a
// stand-in that would agree with whatever it was handed.
func operatePathCipher(t *testing.T) *secrets.Cipher {
	t.Helper()
	cipher, err := secrets.NewCipher(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 32)))
	mustNoErr(t, err, "new cipher")
	return cipher
}

// TestContextCredentialReadIsScopedToTheOwningTenant is the highest-payload
// member of this class: naming a context id alone released that context's k3s
// admin token. The token is written into a placement Secret and handed to a
// Job that runs kubectl against the cluster it authenticates — so the read
// itself is the release, whether or not whatever follows it succeeds.
func TestContextCredentialReadIsScopedToTheOwningTenant(t *testing.T) {
	opsCtx, strangerCtx, opsTenantID, strangerTenantID, db := operationsScopeDatabase(t)
	txs := repository.NewTxManager(db, repository.DialectPostgres)
	contexts := repository.NewContextRepository(txs)
	credentials := repository.NewContextCredentialRepository(txs, operatePathCipher(t))

	strangerContext, err := contexts.Create(strangerCtx, model.Context{Name: "stranger-cluster", Provider: "aws"})
	mustNoErr(t, err, "create stranger context")
	mustNoErr(t, credentials.Set(strangerCtx, strangerContext.ContextID, "stranger-admin-token"), "custody stranger token")

	// The reputation of the fix: an OPERATIONS caller naming only the
	// stranger's context id must not be handed the stranger's token, even
	// though erun_operations' policy makes the row visible to it.
	if _, err := credentials.Get(opsCtx, opsTenantID, strangerContext.ContextID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("credential read for a context the caller's tenant does not own = %v, want ErrNotFound", err)
	}

	// The owning tenant still gets its own token: scoping must not turn into
	// refusing the legitimate read.
	token, err := credentials.Get(opsCtx, strangerTenantID, strangerContext.ContextID)
	mustNoErr(t, err, "read the token on behalf of its owner")
	if token != "stranger-admin-token" {
		t.Fatalf("token = %q, want stranger-admin-token", token)
	}
}

// TestContextReadIsScopedToTheOwningTenant covers the coordinate read every
// placement resolution starts from — project this id, project this server URL,
// project this kubernetes context — which is what the credential above is
// looked up alongside.
func TestContextReadIsScopedToTheOwningTenant(t *testing.T) {
	opsCtx, strangerCtx, opsTenantID, strangerTenantID, db := operationsScopeDatabase(t)
	contexts := repository.NewContextRepository(repository.NewTxManager(db, repository.DialectPostgres))

	strangerContext, err := contexts.Create(strangerCtx, model.Context{Name: "stranger-cluster", Provider: "aws"})
	mustNoErr(t, err, "create stranger context")
	mustNoErr(t, contexts.UpdateProvisioningResult(strangerCtx, strangerContext.ContextID, "running", "i-1234", "203.0.113.10", ""), "record stranger cluster coordinates")

	if _, err := contexts.Get(opsCtx, opsTenantID, strangerContext.ContextID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("context read for a tenant that does not own it = %v, want ErrNotFound", err)
	}

	got, err := contexts.Get(opsCtx, strangerTenantID, strangerContext.ContextID)
	mustNoErr(t, err, "read the context on behalf of its owner")
	if got.ContextID != strangerContext.ContextID || got.PublicIP != "203.0.113.10" {
		t.Fatalf("context = %+v, want the stranger's own row", got)
	}
}
