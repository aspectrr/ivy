package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidatePath_BasicAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	allowedDirs := []string{tmpDir}

	// A file inside the allowed dir
	filePath := filepath.Join(tmpDir, "test.conf")
	if err := os.WriteFile(filePath, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	resolved, err := ValidatePath(filePath, allowedDirs)
	if err != nil {
		t.Fatalf("ValidatePath(%q) error: %v", filePath, err)
	}
	expected, _ := filepath.EvalSymlinks(filePath)
	if expected == "" {
		expected = filePath
	}
	if resolved != expected {
		t.Fatalf("expected %q, got %q", expected, resolved)
	}
}

func TestValidatePath_Subdirectory(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "conf.d")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	allowedDirs := []string{tmpDir}
	resolved, err := ValidatePath(subDir, allowedDirs)
	if err != nil {
		t.Fatalf("ValidatePath(%q) error: %v", subDir, err)
	}
	expected, _ := filepath.EvalSymlinks(subDir)
	if expected == "" {
		expected = subDir
	}
	if resolved != expected {
		t.Fatalf("expected %q, got %q", expected, resolved)
	}
}

func TestValidatePath_BlockedPaths(t *testing.T) {
	allowedDirs := []string{"/etc/logstash"}

	blocked := []string{"/proc/self/cmdline", "/sys/kernel", "/dev/null", "/run/systemd"}
	for _, path := range blocked {
		_, err := ValidatePath(path, allowedDirs)
		if err == nil {
			t.Fatalf("expected error for blocked path %q", path)
		}
	}
}

func TestValidatePath_OutsideAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	allowedDirs := []string{tmpDir}

	_, err := ValidatePath("/etc/passwd", allowedDirs)
	if err == nil {
		t.Fatal("expected error for path outside allowed dirs")
	}
}

func TestValidatePath_DotDotTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "logs")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}

	allowedDirs := []string{subDir}

	// Try to escape with ../../etc/passwd
	escapePath := filepath.Join(subDir, "..", "..", "etc", "passwd")
	_, err := ValidatePath(escapePath, allowedDirs)
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
}

func TestValidatePath_SymlinkWithinAllowed(t *testing.T) {
	tmpDir := t.TempDir()
	realDir := filepath.Join(tmpDir, "real")
	linkDir := filepath.Join(tmpDir, "link")

	if err := os.MkdirAll(realDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatal(err)
	}

	fileInReal := filepath.Join(realDir, "test.conf")
	if err := os.WriteFile(fileInReal, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	// Allow the symlink path — should resolve to real dir and be allowed
	allowedDirs := []string{realDir}
	resolved, err := ValidatePath(filepath.Join(linkDir, "test.conf"), allowedDirs)
	if err != nil {
		t.Fatalf("ValidatePath through symlink: %v", err)
	}
	expected, _ := filepath.EvalSymlinks(fileInReal)
	if expected == "" {
		expected = fileInReal
	}
	if resolved != expected {
		t.Fatalf("expected %q, got %q", expected, resolved)
	}
}

func TestValidatePath_SymlinkEscape(t *testing.T) {
	tmpDir := t.TempDir()
	allowedDir := filepath.Join(tmpDir, "allowed")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create a symlink inside allowed dir pointing outside
	escapeLink := filepath.Join(allowedDir, "escape")
	if err := os.Symlink("/etc", escapeLink); err != nil {
		t.Fatal(err)
	}

	allowedDirs := []string{allowedDir}
	_, err := ValidatePath(filepath.Join(escapeLink, "passwd"), allowedDirs)
	if err == nil {
		t.Fatal("expected error for symlink escaping allowed dir")
	}
}

func TestValidateWorkingDir_Empty(t *testing.T) {
	allowedDirs := []string{"/etc/logstash", "/var/log/logstash"}
	dir, err := ValidateWorkingDir("", allowedDirs)
	if err != nil {
		t.Fatalf("ValidateWorkingDir empty: %v", err)
	}
	if dir != "/etc/logstash" {
		t.Fatalf("expected first allowed dir, got %q", dir)
	}
}

func TestValidateWorkingDir_EmptyNoAllowed(t *testing.T) {
	_, err := ValidateWorkingDir("", nil)
	if err == nil {
		t.Fatal("expected error with no allowed dirs")
	}
}

func TestValidateWorkingDir_Specified(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		t.Fatal(err)
	}
	allowedDirs := []string{tmpDir}

	dir, err := ValidateWorkingDir(tmpDir, allowedDirs)
	if err != nil {
		t.Fatalf("ValidateWorkingDir: %v", err)
	}
	expected, _ := filepath.EvalSymlinks(tmpDir)
	if expected == "" {
		expected = tmpDir
	}
	if dir != expected {
		t.Fatalf("expected %q, got %q", expected, dir)
	}
}
