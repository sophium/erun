package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/adrg/xdg"
)

// ValidateStatePathSegment refuses a value the state tree cannot name a single
// directory after. Every path under the config root is keyed by a tenant and an
// environment, so each has to be one plain path segment: a value carrying
// whitespace, a separator, a NUL, or no characters at all is not a name, and
// joining a path with it does not fail -- it creates a directory beside the
// real one. That is how display-shaped strings accumulate as stray state
// directories nobody ever writes into: "<tenant> <environment>", or that pair
// with a port appended, is how a label and a port-forward read, not how a path
// is spelled. Refusing at the point the path is built tells the caller instead
// of leaving the residue behind.
func ValidateStatePathSegment(kind, value string) error {
	switch {
	case strings.TrimSpace(value) == "":
		return fmt.Errorf("%w: %s is required", ErrUnusableStateName, kind)
	case strings.TrimSpace(value) != value:
		return fmt.Errorf("%w: %s %q has leading or trailing whitespace; pass the name itself", ErrUnusableStateName, kind, value)
	case value == "." || value == "..":
		return fmt.Errorf("%w: %s %q names no directory", ErrUnusableStateName, kind, value)
	}
	for _, r := range value {
		switch {
		case unicode.IsSpace(r):
			return fmt.Errorf("%w: %s %q contains whitespace, so it names no single directory; a tenant and an environment are separate names, not one label", ErrUnusableStateName, kind, value)
		case r == '/' || r == '\\':
			return fmt.Errorf("%w: %s %q contains a path separator", ErrUnusableStateName, kind, value)
		case r == 0:
			return fmt.Errorf("%w: %s %q contains a NUL byte", ErrUnusableStateName, kind, value)
		}
	}
	return nil
}

// validateReadableStatePathSegment is validateStatePathSegment for a read:
// a name the state tree cannot hold is reported the way an environment that was
// never configured is, so listing the tree skips a stray directory instead of
// failing on it, and no read resolves outside the tree.
func validateReadableStatePathSegment(kind, value string) error {
	if err := ValidateStatePathSegment(kind, value); err != nil {
		return fmt.Errorf("%w: %w", ErrNotInitialized, err)
	}
	return nil
}

func ERunConfigDir() (string, error) {
	configHome := strings.TrimSpace(xdg.ConfigHome)
	if configHome == "" {
		return "", ErrNoUserDataFolder
	}
	return filepath.Join(configHome, ConfigRoot), nil
}

// ResolveConfigFilePath resolves the on-disk path of a config file under
// configHome without creating anything. xdg.ConfigFile's own path resolution
// creates the parent directory as a side effect of resolving the path, which
// runs even for a read or a dry-run trace and before any caller has decided
// the write should actually happen. Callers that are about to write still
// need os.MkdirAll immediately before the write.
func ResolveConfigFilePath(relPath string) (string, error) {
	configHome := strings.TrimSpace(xdg.ConfigHome)
	if configHome == "" {
		return "", ErrNoUserDataFolder
	}
	return filepath.Join(configHome, relPath), nil
}

// ResolveCacheFilePath is resolveConfigFilePath's cache-directory
// counterpart: xdg.CacheFile has the same create-the-parent-directory side
// effect on resolution as xdg.ConfigFile, which a mere read or a dry-run
// trace must not trigger. Callers that are about to write still need
// os.MkdirAll immediately before the write.
func ResolveCacheFilePath(relPath string) (string, error) {
	cacheHome := strings.TrimSpace(xdg.CacheHome)
	if cacheHome == "" {
		return "", ErrNoUserDataFolder
	}
	return filepath.Join(cacheHome, relPath), nil
}
