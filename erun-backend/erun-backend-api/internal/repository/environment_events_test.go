package repository

import (
	"errors"
	"testing"
)

func TestEnvironmentEventCursorRoundTripsItsPosition(t *testing.T) {
	cursor := EnvironmentEventCursor{Seq: 918273}
	encoded := cursor.String()
	if encoded != "918273" {
		t.Fatalf("String() = %q, want %q", encoded, "918273")
	}
	parsed, err := ParseEnvironmentEventCursor(encoded)
	if err != nil {
		t.Fatalf("ParseEnvironmentEventCursor(%q): %v", encoded, err)
	}
	if parsed != cursor {
		t.Fatalf("parsed = %+v, want %+v", parsed, cursor)
	}
}

func TestEnvironmentEventCursorZeroEncodesToTheEmptyToken(t *testing.T) {
	if got := (EnvironmentEventCursor{}).String(); got != "" {
		t.Fatalf("zero String() = %q, want empty", got)
	}
	parsed, err := ParseEnvironmentEventCursor("")
	if err != nil {
		t.Fatalf("empty token: %v", err)
	}
	if !parsed.isZero() {
		t.Fatalf("empty token parsed to %+v, want the zero cursor", parsed)
	}
}

func TestParseEnvironmentEventCursorRefusesATokenThatIsNotAPosition(t *testing.T) {
	for _, token := range []string{"not-a-position", "1.5", "-1", "0", " ", "42,7", "1e3"} {
		_, err := ParseEnvironmentEventCursor(token)
		if !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("ParseEnvironmentEventCursor(%q) err = %v, want ErrInvalidInput", token, err)
		}
	}
}
