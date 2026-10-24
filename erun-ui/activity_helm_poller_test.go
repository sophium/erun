package main

import (
	"testing"
	"time"
)

// At deploy start the prior release still reads "deployed" with the old version.
func TestApplyHelmReleaseSnapshotIgnoresStaleVersionOnDeployed(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "local",
		Version:     "1.0.51-snapshot-20260510135933",
		Release:     "erun-devops",
		Namespace:   "erun-local",
		Source:      "trace",
	})

	app.applyHelmReleaseSnapshot("orbstack", helmReleaseSnapshot{
		Name:       "erun-devops",
		Namespace:  "erun-local",
		Status:     "deployed",
		AppVersion: "1.0.50-snapshot-prior",
		Updated:    helmUpdatedNow(t),
	})

	entry, ok := app.activityQueue.findActive("erun", "local")
	if !ok {
		t.Fatal("entry must remain active when helm reports a different AppVersion as deployed")
	}
	if entry.Status != activityQueueStatusRunning {
		t.Fatalf("entry status = %q, want running", entry.Status)
	}
}

// Same-version redeploys need the Updated freshness check.
func TestApplyHelmReleaseSnapshotIgnoresStaleTimestampOnDeployed(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	now := time.Now()
	app.activityQueue.now = func() time.Time { return now }
	app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "local",
		Version:     "1.0.51-snapshot-20260510135933",
		Release:     "erun-devops",
		Namespace:   "erun-local",
		Source:      "trace",
	})

	staleUpdated := now.Add(-(helmDeployedFreshnessSkew + 5*time.Minute))
	app.applyHelmReleaseSnapshot("orbstack", helmReleaseSnapshot{
		Name:       "erun-devops",
		Namespace:  "erun-local",
		Status:     "deployed",
		AppVersion: "1.0.51-snapshot-20260510135933",
		Updated:    staleUpdated.Format("2006-01-02 15:04:05.999999999 -0700 MST"),
	})

	entry, ok := app.activityQueue.findActive("erun", "local")
	if !ok {
		t.Fatal("entry must remain active when helm 'deployed' Updated predates entry.StartedAt")
	}
	if entry.Status != activityQueueStatusRunning {
		t.Fatalf("entry status = %q, want running", entry.Status)
	}
}

func TestApplyHelmReleaseSnapshotFinalizesOnFreshDeployedMatch(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "local",
		Version:     "1.0.51-snapshot-20260510135933",
		Release:     "erun-devops",
		Namespace:   "erun-local",
		Source:      "trace",
	})

	app.applyHelmReleaseSnapshot("orbstack", helmReleaseSnapshot{
		Name:       "erun-devops",
		Namespace:  "erun-local",
		Status:     "deployed",
		AppVersion: "1.0.51-snapshot-20260510135933",
		Updated:    helmUpdatedNow(t),
	})

	if _, stillActive := app.activityQueue.findActive("erun", "local"); stillActive {
		t.Fatal("entry should be finalized when version matches and Updated is fresh")
	}
	history := app.activityQueue.list()
	if len(history) != 1 || history[0].Status != activityQueueStatusSucceeded {
		t.Fatalf("expected one succeeded entry in history, got %+v", history)
	}
}

// The version gate applies only to "deployed".
func TestApplyHelmReleaseSnapshotFinalizesOnFailedRegardlessOfVersion(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	app.activityQueue.start(activityQueueEntry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "local",
		Version:     "1.0.51-snapshot-20260510135933",
		Release:     "erun-devops",
		Namespace:   "erun-local",
		Source:      "trace",
	})

	app.applyHelmReleaseSnapshot("orbstack", helmReleaseSnapshot{
		Name:       "erun-devops",
		Namespace:  "erun-local",
		Status:     "failed",
		AppVersion: "1.0.50-snapshot-prior",
	})

	if _, stillActive := app.activityQueue.findActive("erun", "local"); stillActive {
		t.Fatal("entry should be finalized when helm reports failed")
	}
	history := app.activityQueue.list()
	if len(history) != 1 || history[0].Status != activityQueueStatusFailed {
		t.Fatalf("expected one failed entry in history, got %+v", history)
	}
}

func TestParseHelmUpdatedAcceptsHelmFormats(t *testing.T) {
	cases := []string{
		"2026-05-10 17:00:26.926452 +0300 EEST",
		"2026-05-10 17:00:26 +0300 EEST",
		"2026-05-10 17:00:26.926452 +0300",
		"2026-05-10T17:00:26.926452+03:00",
		"2026-05-10T17:00:26+03:00",
	}
	for _, value := range cases {
		if _, ok := parseHelmUpdated(value); !ok {
			t.Errorf("parseHelmUpdated rejected %q", value)
		}
	}
	if _, ok := parseHelmUpdated(""); ok {
		t.Error("parseHelmUpdated must reject empty input")
	}
	if _, ok := parseHelmUpdated("not a timestamp"); ok {
		t.Error("parseHelmUpdated must reject garbage input")
	}
}

// helm v4 removed `--all`; using it makes every poll fail silently.
func TestHelmListArgsAvoidsDeprecatedAllFlag(t *testing.T) {
	args := helmListTenantDevopsArgs("erun")
	for _, a := range args {
		if a == "--all" {
			t.Fatalf("--all is deprecated in helm v4; args = %v", args)
		}
	}
	for _, want := range []string{"--deployed", "--pending", "--failed", "--uninstalling"} {
		if !containsString(args, want) {
			t.Errorf("missing %s in helm list args: %v", want, args)
		}
	}
	if !containsPair(args, "--kube-context", "erun") {
		t.Errorf("expected --kube-context erun in args: %v", args)
	}
	bare := helmListTenantDevopsArgs("")
	if containsString(bare, "--kube-context") {
		t.Errorf("empty kube context should not append --kube-context; args = %v", bare)
	}
}

func containsString(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func containsPair(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func helmUpdatedNow(t *testing.T) string {
	t.Helper()
	return time.Now().Format("2006-01-02 15:04:05.999999999 -0700 MST")
}
