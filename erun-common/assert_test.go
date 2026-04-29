package eruncommon

import (
	"reflect"
	"strings"
	"testing"
)

func requireNoError(t *testing.T, err error, message string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", message, err)
	}
}

func requireEqual[T comparable](t *testing.T, got, want T, message string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %v want %v", message, got, want)
	}
}

func requireDeepEqual(t *testing.T, got, want any, message string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %+v want %+v", message, got, want)
	}
}

func requireStringContains(t *testing.T, got, want, message string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("%s: %s", message, got)
	}
}

func requireBytesContains(t *testing.T, got []byte, want string, message string) {
	t.Helper()
	if !strings.Contains(string(got), want) {
		t.Fatalf("%s: %s", message, got)
	}
}

func requireCondition(t *testing.T, ok bool, message string, args ...any) {
	t.Helper()
	if !ok {
		t.Fatalf(message, args...)
	}
}
