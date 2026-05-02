package backendapi

import "testing"

func TestNewExternalIDReturnsUUIDv7(t *testing.T) {
	id, err := NewExternalID()
	if err != nil {
		t.Fatalf("NewExternalID failed: %v", err)
	}

	if err := ValidateExternalID(id); err != nil {
		t.Fatalf("ValidateExternalID failed: %v", err)
	}
}

func TestValidateExternalIDRejectsNonUUIDv7(t *testing.T) {
	err := ValidateExternalID("550e8400-e29b-41d4-a716-446655440000")
	if err == nil {
		t.Fatal("expected non-v7 UUID to be rejected")
	}
}
