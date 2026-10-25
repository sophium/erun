package envops

import (
	"testing"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// A Windows ConPTY repaint re-sends "==> Initialized"; it must fire once per env.
func TestEnvironmentInitializedFiresOncePerEnv(t *testing.T) {
	emits := newCapturedEmits()
	service := newTestService(Deps{Emit: emits.fn()})

	service.EnvironmentInitialized("frs", "dev")
	service.EnvironmentInitialized("frs", "dev")
	service.EnvironmentInitialized("frs", "dev")
	if got := len(emits.events(uimodel.EnvironmentInitializedEvent)); got != 1 {
		t.Fatalf("environment-initialized fired %d times for one env, want 1 (repaints must not re-fire)", got)
	}

	service.EnvironmentInitialized("frs", "other")
	if got := len(emits.events(uimodel.EnvironmentInitializedEvent)); got != 2 {
		t.Fatalf("distinct env total = %d, want 2", got)
	}

	// Delete/failure resets, so a re-create fires again.
	service.ClearInitEmitted("frs", "dev")
	service.EnvironmentInitialized("frs", "dev")
	if got := len(emits.events(uimodel.EnvironmentInitializedEvent)); got != 3 {
		t.Fatalf("re-create after reset total = %d, want 3", got)
	}
}
