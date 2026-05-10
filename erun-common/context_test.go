package eruncommon

import (
	"errors"
	"testing"
)

func TestRequireKubernetesContextRejectsEmpty(t *testing.T) {
	ctx := Context{}
	if err := ctx.RequireKubernetesContext(""); err == nil {
		t.Fatal("RequireKubernetesContext(\"\") should error to prevent silent fallthrough to current-context")
	}
	if err := ctx.RequireKubernetesContext("   "); err == nil {
		t.Fatal("whitespace-only context should be rejected the same as empty")
	}
}

func TestRequireKubernetesContextRunsPreflightOnNonEmpty(t *testing.T) {
	got := ""
	ctx := Context{
		KubernetesContextPreflight: func(_ Context, name string) error {
			got = name
			return nil
		},
	}
	if err := ctx.RequireKubernetesContext("erun"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "erun" {
		t.Fatalf("preflight saw %q, want %q", got, "erun")
	}
}

func TestRequireKubernetesContextSurfacesPreflightErrors(t *testing.T) {
	want := errors.New("kubeconfig missing")
	ctx := Context{
		KubernetesContextPreflight: func(Context, string) error { return want },
	}
	err := ctx.RequireKubernetesContext("erun")
	if !errors.Is(err, want) {
		t.Fatalf("expected preflight error to surface, got %v", err)
	}
}

func TestEnsureKubernetesContextStaysAdvisoryOnEmpty(t *testing.T) {
	called := false
	ctx := Context{
		KubernetesContextPreflight: func(Context, string) error {
			called = true
			return nil
		},
	}
	if err := ctx.EnsureKubernetesContext(""); err != nil {
		t.Fatalf("Ensure should remain a no-op on empty: %v", err)
	}
	if called {
		t.Fatal("Ensure must not invoke preflight on empty context")
	}
}
