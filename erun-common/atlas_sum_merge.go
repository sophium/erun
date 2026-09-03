package eruncommon

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// atlasSumBasename is the file name Atlas writes for a migration directory's
// integrity checksum (erun-backend-db/AGENTS.md's "Atlas Workflow"). It is a
// hash chain over every migration file in that directory, so two branches
// that each add a migration necessarily produce a different final line —
// this file conflicts on every concurrent pair of migrations by
// construction, and the conflict carries no information about which side is
// "right". The only correct resolution is always to regenerate it from the
// migration files that actually land on disk, the same integrity check
// `atlas migrate apply`/`validate` perform at deploy time — never to
// hand-merge the hash lines, which produces a file that looks resolved and
// then fails at migration time instead of at merge time.
const atlasSumBasename = "atlas.sum"

// AtlasMigrateHashRunnerFunc regenerates one Atlas migration directory's
// atlas.sum from the .sql files actually present there. Injectable so tests
// can fake atlas without a live install.
type AtlasMigrateHashRunnerFunc func(root, migrationsDir string) error

// runAtlasMigrateHash shells out to the real `atlas migrate hash` — the same
// tool `atlas migrate apply`/`validate` trust to detect tampering — so a
// regenerated atlas.sum is exactly what atlas itself would have written for
// the migration files on disk, never an approximation erun invents.
func runAtlasMigrateHash(root, migrationsDir string) error {
	cmd := Command("atlas", "migrate", "hash", "--dir", "file://"+filepath.ToSlash(migrationsDir))
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("atlas migrate hash --dir %s: %w: %s", migrationsDir, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// resolveAtlasSumConflicts regenerates and stages every conflicted atlas.sum
// among conflicted, returning whatever conflicts remain (empty once every
// conflict was an atlas.sum this could regenerate). A conflicted atlas.sum
// needs only its own directory of .sql files to regenerate correctly, and
// those are already the real merge result on disk: the squash/merge that
// produced the conflict already checked out every non-conflicting file from
// both sides before reporting failure, so regenerating from disk can never
// disagree with what actually landed.
func resolveAtlasSumConflicts(root string, runGit GitCommandRunnerFunc, runAtlasHash AtlasMigrateHashRunnerFunc, conflicted []string) ([]string, error) {
	var remaining []string
	for _, file := range conflicted {
		if filepath.Base(file) != atlasSumBasename {
			remaining = append(remaining, file)
			continue
		}
		migrationsDir := filepath.Dir(file)
		if err := runAtlasHash(root, migrationsDir); err != nil {
			return nil, fmt.Errorf("regenerate %s: %w", file, err)
		}
		var addStderr bytes.Buffer
		if err := runGit(root, io.Discard, &addStderr, "add", "--", file); err != nil {
			return nil, fmt.Errorf("git add %s: %w: %s", file, err, strings.TrimSpace(addStderr.String()))
		}
	}
	return remaining, nil
}
