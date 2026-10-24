package roleclassification

import (
	"reflect"
	"testing"
)

func TestUnclassifiedFindsRoutesMissingFromTheMap(t *testing.T) {
	routes := []string{"GET /v1/a", "POST /v1/b", "DELETE /v1/c"}
	classified := map[string]bool{"GET /v1/a": true, "DELETE /v1/c": true}

	got := Unclassified(routes, classified)
	want := []string{"POST /v1/b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Unclassified() = %v, want %v", got, want)
	}
}

func TestUnclassifiedIsEmptyWhenEveryRouteIsClassified(t *testing.T) {
	routes := []string{"GET /v1/a", "POST /v1/b"}
	classified := map[string]bool{"GET /v1/a": true, "POST /v1/b": true}

	if got := Unclassified(routes, classified); len(got) != 0 {
		t.Fatalf("expected no unclassified routes, got %v", got)
	}
}

func TestUnclassifiedReportsMultipleMissingRoutesSorted(t *testing.T) {
	routes := []string{"POST /v1/z", "GET /v1/a", "DELETE /v1/m"}
	classified := map[string]bool{}

	got := Unclassified(routes, classified)
	want := []string{"DELETE /v1/m", "GET /v1/a", "POST /v1/z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Unclassified() = %v, want %v", got, want)
	}
}
