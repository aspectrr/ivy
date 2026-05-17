package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidatePath ensures a path is within one of the allowed directories.
// It resolves symlinks and cleans the path before checking.
func ValidatePath(path string, allowedDirs []string) (string, error) {
	// Clean the path
	cleaned := filepath.Clean(path)

	// Get absolute path
	absPath, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("resolving absolute path: %w", err)
	}

	// Resolve symlinks
	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("resolving symlinks: %w", err)
	}

	// Block sensitive paths
	blocked := []string{"/proc", "/sys", "/dev", "/run", "/tmp/ivy-"}
	for _, prefix := range blocked {
		if strings.HasPrefix(resolved, prefix) {
			return "", fmt.Errorf("path %q is in a blocked directory", path)
		}
	}

	// Check against allowed directories
	for _, dir := range allowedDirs {
		allowedAbs, err := filepath.Abs(filepath.Clean(dir))
		if err != nil {
			continue
		}
		allowedResolved, err := filepath.EvalSymlinks(allowedAbs)
		if err != nil {
			// Directory may not exist yet on leaf — use the cleaned path
			allowedResolved = allowedAbs
		}
		// Ensure the allowed dir itself or anything under it
		if resolved == allowedResolved || strings.HasPrefix(resolved, allowedResolved+string(os.PathSeparator)) {
			return resolved, nil
		}
	}

	return "", fmt.Errorf("path %q is outside allowed directories", path)
}

// ValidateWorkingDir is a convenience that validates a working directory.
// If workingDir is empty, it returns the first allowed directory (or an error).
func ValidateWorkingDir(workingDir string, allowedDirs []string) (string, error) {
	if workingDir == "" {
		if len(allowedDirs) == 0 {
			return "", fmt.Errorf("no allowed directories configured")
		}
		return allowedDirs[0], nil
	}
	return ValidatePath(workingDir, allowedDirs)
}
