package fingerprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The erun-blueprint-api skill copies this go.mod into tenant repositories, and
// go mod tidy raises a pin only as far as a dependency requires, so a scaffolded
// service runs these releases. Each floor is the release from which no known advisory applies; a
// pin below it brings them back. go-jose, x/crypto and otel are indirect, and
// no direct dependency requires a fixed release of them yet.
var blueprintAPIModuleSecurityFloors = []struct {
	module, floor, fixes string
}{
	{"github.com/coreos/go-oidc/v3", "3.15.0", "decoding a bearer token's claims before its signature is checked, after splitting it on every dot"},
	{"github.com/go-jose/go-jose/v4", "4.1.6", "GO-2025-3485 (unbounded splitting of a compact JWS, reachable before authentication), GO-2026-4945 and GHSA-mgcc-cww5-fp2q; go-oidc requires only 4.1.4"},
	{"github.com/uptrace/bun/driver/pgdriver", "1.2.16", "GO-2025-3765 (SQL injection through a negative query argument) and the string-literal injection against a server running with standard_conforming_strings off"},
	{"golang.org/x/oauth2", "0.27.0", "GO-2025-3488"},
	{"golang.org/x/crypto", "0.56.0", "GO-2026-6303, GO-2026-6354, GO-2026-6355 and every earlier fixed x/crypto advisory; pgdriver requires only 0.48.0"},
	{"golang.org/x/sys", "0.44.0", "GO-2026-5024"},
	{"go.opentelemetry.io/otel", "1.44.0", "GO-2026-5506 and GO-2026-5158, which 1.43.0 reintroduced after 1.42.0 fixed it; pgdriver requires only 1.40.0"},
}

var blueprintAPIGoModTemplate = filepath.Join("erun-skills", "skills", "erun-blueprint-api", "templates", "go.mod.tmpl")

// goModDirectives returns each require and replace line of a go.mod as its
// directive and fields, whether written as one line or inside a block.
func goModDirectives(t *testing.T, path string) [][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var directives [][]string
	block := ""
	for _, line := range strings.Split(string(data), "\n") {
		var directive []string
		block, directive = goModLine(block, line)
		if directive != nil {
			directives = append(directives, directive)
		}
	}
	return directives
}

// goModLine reads one line inside block ("" outside any block) and returns
// the block the next line is in and the directive this line holds, if any.
func goModLine(block, line string) (string, []string) {
	if i := strings.Index(line, "//"); i >= 0 {
		line = line[:i]
	}
	fields := strings.Fields(line)
	switch {
	case len(fields) == 0:
		return block, nil
	case block != "" && fields[0] == ")":
		return "", nil
	case block == "" && len(fields) == 2 && fields[1] == "(":
		return fields[0], nil
	case block != "":
		return block, append([]string{block}, fields...)
	default:
		return block, fields
	}
}

func TestBlueprintAPITemplatePinsItsModulesAtOrAboveTheirSecurityFloors(t *testing.T) {
	requires := map[string]string{}
	for _, directive := range goModDirectives(t, filepath.Join(erunRepoRoot(t), blueprintAPIGoModTemplate)) {
		switch {
		case directive[0] == "replace":
			t.Errorf("%s carries replace %s; a replace can swap a pinned module for any release, so the template must not carry one", blueprintAPIGoModTemplate, strings.Join(directive[1:], " "))
		case directive[0] == "require" && len(directive) == 3:
			requires[directive[1]] = directive[2]
		}
	}
	for _, floor := range blueprintAPIModuleSecurityFloors {
		minimum, _ := leadingVersion(floor.floor)
		pinned, ok := requires[floor.module]
		if !ok {
			t.Errorf("%s no longer requires %s; without that pin a scaffolded service resolves whatever its dependencies ask for, and nothing holds it at %s, the release that drops %s", blueprintAPIGoModTemplate, floor.module, floor.floor, floor.fixes)
			continue
		}
		version, ok := leadingVersion(pinned)
		if !ok {
			t.Errorf("%s: cannot read a version from %s %s", blueprintAPIGoModTemplate, floor.module, pinned)
			continue
		}
		if versionBelow(version, minimum) {
			t.Errorf("%s pins %s %s, below %s, the release that drops %s", blueprintAPIGoModTemplate, floor.module, pinned, floor.floor, floor.fixes)
		}
	}
}
