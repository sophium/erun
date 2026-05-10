package repository

import "testing"

func TestTxManagerDefaultsToPostgres(t *testing.T) {
	txs := NewTxManager(nil, "")
	if txs.dialect != DialectPostgres {
		t.Fatalf("expected default dialect %q, got %q", DialectPostgres, txs.dialect)
	}
}
