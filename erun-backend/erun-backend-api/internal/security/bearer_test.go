package security

import (
	"strings"
	"testing"
)

// fieldsBearerToken is the parse BearerToken replaced, kept as the acceptance oracle:
// it collects every field of the header before checking there are two.
func fieldsBearerToken(header string) (string, bool) {
	fields := strings.Fields(header)
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bearer") {
		return "", false
	}
	return fields[1], true
}

// The shapes that decide acceptance: field count, scheme case, ASCII and Unicode
// whitespace on either side, characters that look like space but are not, and
// invalid UTF-8.
var bearerHeaderShapes = []string{
	"",
	" ",
	"Bearer",
	"Bearer ",
	"Bearer abc",
	"bearer abc",
	"BEARER abc",
	"BeArEr abc",
	"  Bearer   abc  ",
	"\tBearer\tabc\n",
	"Bearer\vabc\f",
	"Bearer\rabc\r\n",
	"Bearer abc def",
	"Bearer abc def ghi",
	"Bearer abc def ",
	"Basic abc",
	"Bearerabc",
	"abc Bearer",
	"Bearer\u00a0abc",
	"Bearer\u0085abc",
	"Bearer\u1680abc",
	"Bearer\u2003abc\u2028",
	"\u3000Bearer\u3000abc\u3000",
	"Bearer abc\u200b",
	"Bearer\u200babc",
	"Bearer \xffabc",
	"Bearer\xa0abc",
	"Bearer \xc3",
	"Bearer é",
	"é Bearer",
	"Bearer a\u00a0b",
}

func TestBearerTokenAcceptsExactlyWhatSplittingEveryFieldAccepted(t *testing.T) {
	for _, header := range bearerHeaderShapes {
		token, ok := BearerToken(header)
		wantToken, wantOK := fieldsBearerToken(header)
		if token != wantToken || ok != wantOK {
			t.Errorf("BearerToken(%q) = (%q, %v), want (%q, %v)", header, token, ok, wantToken, wantOK)
		}
	}
}

func TestBearerTokenReadsTheSecondFieldOfABearerPair(t *testing.T) {
	cases := map[string]string{
		"Bearer abc":            "abc",
		"bearer abc":            "abc",
		"  Bearer \t abc.def  ": "abc.def",
		"Bearer\u3000abc":       "abc",
	}
	for header, want := range cases {
		if token, ok := BearerToken(header); !ok || token != want {
			t.Errorf("BearerToken(%q) = (%q, %v), want (%q, true)", header, token, ok, want)
		}
	}
	for _, header := range []string{"", "Bearer", "Bearer a b", "Basic abc", "Bearer\u200babc"} {
		if token, ok := BearerToken(header); ok {
			t.Errorf("BearerToken(%q) accepted token %q, want it refused", header, token)
		}
	}
}

func FuzzBearerTokenAcceptsExactlyWhatSplittingEveryFieldAccepted(f *testing.F) {
	for _, header := range bearerHeaderShapes {
		f.Add(header)
	}
	f.Fuzz(func(t *testing.T, header string) {
		token, ok := BearerToken(header)
		wantToken, wantOK := fieldsBearerToken(header)
		if token != wantToken || ok != wantOK {
			t.Errorf("BearerToken(%q) = (%q, %v), want (%q, %v)", header, token, ok, wantToken, wantOK)
		}
	})
}
