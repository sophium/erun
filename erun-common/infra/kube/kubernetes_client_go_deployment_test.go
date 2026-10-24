package kube

import (
	"net/http"
	"strings"
	"testing"
)

func deploymentFoundHandler(namespace, name string) http.HandlerFunc {
	path := "/apis/apps/v1/namespaces/" + namespace + "/deployments/" + name
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"NotFound"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"` + name + `","namespace":"` + namespace + `"}}`))
	}
}

func deploymentNotFoundHandler(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
			`"message":"deployments.apps \"` + name + `\" not found","reason":"NotFound","code":404}`))
	}
}

func TestLibraryDeploymentPresentReportsNotFoundAsAbsentNotError(t *testing.T) {
	fakeKubernetesAPIServer(t, deploymentNotFoundHandler("team-api"))

	present, err := libraryDeploymentPresent("", "team-dev", "team-api")
	if err != nil {
		t.Fatalf("libraryDeploymentPresent: %v", err)
	}
	if present {
		t.Fatalf("present = true, want false")
	}
}

func TestLibraryDeploymentPresentPropagatesOtherErrors(t *testing.T) {
	fakeKubernetesAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
			`"message":"deployments.apps \"team-api\" is forbidden","reason":"Forbidden","code":403}`))
	})

	present, err := libraryDeploymentPresent("", "team-dev", "team-api")
	if err == nil {
		t.Fatalf("err = nil, want a forbidden error")
	}
	if present {
		t.Fatalf("present = true, want false")
	}
}

func TestLibraryDeploymentPresentHonorsContextOverride(t *testing.T) {
	fakeKubernetesAPIServer(t, deploymentFoundHandler("team-dev", "team-api"))

	present, err := libraryDeploymentPresent("test-context", "team-dev", "team-api")
	if err != nil {
		t.Fatalf("libraryDeploymentPresent: %v", err)
	}
	if !present {
		t.Fatalf("present = false, want true")
	}

	if _, err := libraryDeploymentPresent("unknown-context", "team-dev", "team-api"); err == nil {
		t.Fatalf("err = nil, want an error for an unknown context")
	}
}

// deploymentConditionHandler reports Available=False for the first failCount requests, then True.
func deploymentConditionHandler(namespace, name string, failCount int) http.HandlerFunc {
	path := "/apis/apps/v1/namespaces/" + namespace + "/deployments/" + name
	calls := 0
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"NotFound"}`))
			return
		}
		calls++
		status := "False"
		if calls > failCount {
			status = "True"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"` + name + `","namespace":"` + namespace + `"},` +
			`"status":{"conditions":[{"type":"Available","status":"` + status + `"}]}}`))
	}
}

func TestLibraryWaitForDeploymentAvailablePollsUntilConditionBecomesTrue(t *testing.T) {
	t.Setenv("ERUN_KUBECTL_DEPLOYMENT_WAIT_POLL_INTERVAL", "5ms")
	fakeKubernetesAPIServer(t, deploymentConditionHandler("team-dev", "team-api", 2))

	if err := LibraryWaitForDeploymentAvailable("", "team-dev", "team-api", "5s"); err != nil {
		t.Fatalf("libraryWaitForDeploymentAvailable: %v", err)
	}
}

func TestLibraryWaitForDeploymentAvailableToleratesNotFoundUntilCreated(t *testing.T) {
	t.Setenv("ERUN_KUBECTL_DEPLOYMENT_WAIT_POLL_INTERVAL", "5ms")
	calls := 0
	fakeKubernetesAPIServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls <= 2 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"kind":"Status","apiVersion":"v1","status":"Failure",` +
				`"message":"deployments.apps \"team-api\" not found","reason":"NotFound","code":404}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"Deployment","apiVersion":"apps/v1","metadata":{"name":"team-api","namespace":"team-dev"},` +
			`"status":{"conditions":[{"type":"Available","status":"True"}]}}`))
	})

	if err := LibraryWaitForDeploymentAvailable("", "team-dev", "team-api", "5s"); err != nil {
		t.Fatalf("libraryWaitForDeploymentAvailable: %v", err)
	}
}

func TestLibraryWaitForDeploymentAvailableTimesOutWhenConditionNeverTrue(t *testing.T) {
	t.Setenv("ERUN_KUBECTL_DEPLOYMENT_WAIT_POLL_INTERVAL", "5ms")
	fakeKubernetesAPIServer(t, deploymentConditionHandler("team-dev", "team-api", 1<<20))

	err := LibraryWaitForDeploymentAvailable("", "team-dev", "team-api", "20ms")
	if err == nil {
		t.Fatalf("err = nil, want a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %q, want it to mention a timeout", err)
	}
}

func TestLibraryWaitForDeploymentAvailablePropagatesOtherErrors(t *testing.T) {
	fakeKubernetesAPIServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"kind":"Status","status":"Failure","reason":"InternalError"}`))
	})

	err := LibraryWaitForDeploymentAvailable("", "team-dev", "team-api", "5s")
	if err == nil {
		t.Fatalf("err = nil, want an error")
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %q, want the underlying API error, not a timeout", err)
	}
}

func TestLibraryWaitForDeploymentAvailableRejectsInvalidTimeout(t *testing.T) {
	if err := LibraryWaitForDeploymentAvailable("", "team-dev", "team-api", "not-a-duration"); err == nil {
		t.Fatalf("err = nil, want an error for an invalid timeout")
	}
}
