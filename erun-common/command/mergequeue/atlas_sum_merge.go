package mergequeue

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/gitcmd"
)

// atlasSumBasename is a hash chain over its directory's migrations, so it conflicts on
// every concurrent migration pair; always regenerate it from disk, never hand-merge.
const atlasSumBasename = "atlas.sum"

// AtlasMigrateHashRunnerFunc regenerates one migration directory's atlas.sum.
type AtlasMigrateHashRunnerFunc func(root, migrationsDir string) error

func runAtlasMigrateHash(root, migrationsDir string) error {
	cmd := core.Command("atlas", "migrate", "hash", "--dir", "file://"+filepath.ToSlash(migrationsDir))
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("atlas migrate hash --dir %s: %w: %s", migrationsDir, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// resolveAtlasSumConflicts regenerates and stages each conflicted atlas.sum, returning
// the other conflicts. The .sql files on disk are already the merge result.
func resolveAtlasSumConflicts(root string, runGit gitcmd.GitCommandRunnerFunc, runAtlasHash AtlasMigrateHashRunnerFunc, conflicted []string) ([]string, error) {
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
