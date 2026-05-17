package sync

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/aspectrr/ivy/internal/leaf/commands"
)

// FileInfo describes a file to be synced.
type FileInfo struct {
	Path        string `json:"path"`
	Size        int64  `json:"size"`
	ChecksumSHA string `json:"checksum_sha256"`
}

// SyncResult holds the result of a directory sync.
type SyncResult struct {
	Files   []FileInfo
	TarData []byte
}

// Directory syncs the contents of a directory to a tar archive.
// Only files within allowed directories are included.
func Directory(dir string, allowedDirs []string) (*SyncResult, error) {
	// Validate the directory path
	resolved, err := commands.ValidatePath(dir, allowedDirs)
	if err != nil {
		return nil, fmt.Errorf("invalid directory: %w", err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", resolved)
	}

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	defer func() { _ = tw.Close() }()

	var files []FileInfo

	err = filepath.Walk(resolved, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Skip directories — tar handles them via file paths
		if fi.IsDir() {
			return nil
		}

		// Verify the file is still within allowed dirs (in case of symlink tricks)
		cleanPath, err := commands.ValidatePath(path, allowedDirs)
		if err != nil {
			// Skip files that fail validation rather than aborting the whole sync
			return nil
		}

		// Open and hash the file
		f, err := os.Open(cleanPath)
		if err != nil {
			return fmt.Errorf("opening %q: %w", cleanPath, err)
		}
		defer func() { _ = f.Close() }()

		hasher := sha256.New()
		var fileBuf bytes.Buffer
		if _, err := io.Copy(io.MultiWriter(hasher, &fileBuf), f); err != nil {
			return fmt.Errorf("reading %q: %w", cleanPath, err)
		}
		_ = f.Close()

		// Relative path within the synced directory
		relPath, err := filepath.Rel(resolved, cleanPath)
		if err != nil {
			relPath = cleanPath
		}

		// Write tar header
		header := &tar.Header{
			Name:    relPath,
			Size:    fi.Size(),
			Mode:    int64(fi.Mode()),
			ModTime: fi.ModTime(),
		}
		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("tar header for %q: %w", relPath, err)
		}
		if _, err := tw.Write(fileBuf.Bytes()); err != nil {
			return fmt.Errorf("tar write for %q: %w", relPath, err)
		}

		files = append(files, FileInfo{
			Path:        relPath,
			Size:        fi.Size(),
			ChecksumSHA: fmt.Sprintf("%x", hasher.Sum(nil)),
		})

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walking directory: %w", err)
	}

	if err := tw.Flush(); err != nil {
		return nil, fmt.Errorf("flushing tar: %w", err)
	}

	return &SyncResult{
		Files:   files,
		TarData: buf.Bytes(),
	}, nil
}

// Files returns a listing of files in a directory with checksums, without
// creating a tar archive. Useful for incremental sync comparisons.
func Files(dir string, allowedDirs []string) ([]FileInfo, error) {
	resolved, err := commands.ValidatePath(dir, allowedDirs)
	if err != nil {
		return nil, fmt.Errorf("invalid directory: %w", err)
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("stat directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%q is not a directory", resolved)
	}

	var files []FileInfo

	err = filepath.Walk(resolved, func(path string, fi os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if fi.IsDir() {
			return nil
		}

		cleanPath, err := commands.ValidatePath(path, allowedDirs)
		if err != nil {
			return nil // skip invalid
		}

		// Hash the file
		hash, err := fileHash(cleanPath)
		if err != nil {
			return nil // skip unreadable files
		}

		relPath, err := filepath.Rel(resolved, cleanPath)
		if err != nil {
			relPath = cleanPath
		}

		files = append(files, FileInfo{
			Path:        relPath,
			Size:        fi.Size(),
			ChecksumSHA: hash,
		})
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walking directory: %w", err)
	}

	return files, nil
}

// fileHash computes the SHA-256 hash of a file.
func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

// Diff compares two file lists and returns files that are new or changed.
func Diff(previous, current []FileInfo) []FileInfo {
	prevMap := make(map[string]FileInfo, len(previous))
	for _, f := range previous {
		prevMap[f.Path] = f
	}

	var changed []FileInfo
	for _, f := range current {
		prev, exists := prevMap[f.Path]
		if !exists || prev.ChecksumSHA != f.ChecksumSHA {
			changed = append(changed, f)
		}
	}
	return changed
}

// ValidateTarData checks that a tar archive only contains paths without
// path traversal components.
func ValidateTarData(tarData []byte) error {
	tr := tar.NewReader(bytes.NewReader(tarData))
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("reading tar: %w", err)
		}
		// Block path traversal
		if strings.Contains(header.Name, "..") {
			return fmt.Errorf("tar contains path traversal: %q", header.Name)
		}
	}
	return nil
}
