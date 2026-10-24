package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/adrg/xdg"
)

// ValidateStatePathSegment refuses a value that is not one plain path segment,
// since joining it would silently create a stray directory beside the real one.
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

// validateReadableStatePathSegment reports an unusable name as ErrNotInitialized,
// so listing skips stray directories and no read resolves outside the tree.
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

// ResolveConfigFilePath resolves a config path without creating the parent
// directory, unlike xdg.ConfigFile; writers must still os.MkdirAll first.
func ResolveConfigFilePath(relPath string) (string, error) {
	configHome := strings.TrimSpace(xdg.ConfigHome)
	if configHome == "" {
		return "", ErrNoUserDataFolder
	}
	return filepath.Join(configHome, relPath), nil
}

// ResolveCacheFilePath is ResolveConfigFilePath for the cache directory.
func ResolveCacheFilePath(relPath string) (string, error) {
	cacheHome := strings.TrimSpace(xdg.CacheHome)
	if cacheHome == "" {
		return "", ErrNoUserDataFolder
	}
	return filepath.Join(cacheHome, relPath), nil
}
