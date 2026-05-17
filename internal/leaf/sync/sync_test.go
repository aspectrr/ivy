package sync

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestDirectory_Basic(t *testing.T) {
	dir := t.TempDir()
	allowedDirs := []string{dir}

	// Create test files
	if err := os.WriteFile(filepath.Join(dir, "a.conf"), []byte("input {}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.conf"), []byte("output {}"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "c.conf"), []byte("filter {}"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := Directory(dir, allowedDirs)
	if err != nil {
		t.Fatalf("Directory: %v", err)
	}

	if len(result.Files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(result.Files))
	}

	// Verify tar data is valid
	if len(result.TarData) == 0 {
		t.Fatal("expected non-empty tar data")
	}

	// Read back the tar
	tr := tar.NewReader(bytes.NewReader(result.TarData))
	var tarFiles []string
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading tar: %v", err)
		}
		tarFiles = append(tarFiles, header.Name)
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, tr); err != nil {
			t.Fatalf("reading tar entry: %v", err)
		}
		t.Logf("  tar entry: %s (%d bytes)", header.Name, buf.Len())
	}

	sort.Strings(tarFiles)
	expected := []string{"a.conf", "b.conf", "sub/c.conf"}
	sort.Strings(expected)
	if len(tarFiles) != len(expected) {
		t.Fatalf("tar files: got %v, want %v", tarFiles, expected)
	}
	for i, name := range expected {
		if tarFiles[i] != name {
			t.Fatalf("tar file[%d]: got %q, want %q", i, tarFiles[i], name)
		}
	}
}

func TestDirectory_FileChecksums(t *testing.T) {
	dir := t.TempDir()
	allowedDirs := []string{dir}

	content := "hello world"
	if err := os.WriteFile(filepath.Join(dir, "test.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := Directory(dir, allowedDirs)
	if err != nil {
		t.Fatalf("Directory: %v", err)
	}

	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(result.Files))
	}
	if result.Files[0].ChecksumSHA == "" {
		t.Fatal("expected non-empty checksum")
	}
	if result.Files[0].Size != int64(len(content)) {
		t.Fatalf("expected size %d, got %d", len(content), result.Files[0].Size)
	}
}

func TestDirectory_InvalidPath(t *testing.T) {
	dir := t.TempDir()
	allowedDirs := []string{dir}

	_, err := Directory("/etc/passwd", allowedDirs)
	if err == nil {
		t.Fatal("expected error for path outside allowed dirs")
	}
}

func TestDirectory_NotADirectory(t *testing.T) {
	dir := t.TempDir()
	allowedDirs := []string{dir}

	filePath := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(filePath, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Directory(filePath, allowedDirs)
	if err == nil {
		t.Fatal("expected error for non-directory path")
	}
}

func TestDirectory_SkipsInvalidSymlinks(t *testing.T) {
	dir := t.TempDir()
	allowedDirs := []string{dir}

	// Create a valid file
	if err := os.WriteFile(filepath.Join(dir, "good.txt"), []byte("good"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create a symlink pointing outside allowed dir
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "bad_link")); err != nil {
		t.Fatal(err)
	}

	result, err := Directory(dir, allowedDirs)
	if err != nil {
		t.Fatalf("Directory: %v", err)
	}

	// Should only have the good file, not the symlink
	if len(result.Files) != 1 {
		t.Fatalf("expected 1 file (symlink skipped), got %d", len(result.Files))
	}
	if result.Files[0].Path != "good.txt" {
		t.Fatalf("expected good.txt, got %s", result.Files[0].Path)
	}
}

func TestFiles_Basic(t *testing.T) {
	dir := t.TempDir()
	allowedDirs := []string{dir}

	if err := os.WriteFile(filepath.Join(dir, "a.conf"), []byte("aaa"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.conf"), []byte("bbb"), 0644); err != nil {
		t.Fatal(err)
	}

	files, err := Files(dir, allowedDirs)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	for _, f := range files {
		if f.ChecksumSHA == "" {
			t.Fatalf("file %q has no checksum", f.Path)
		}
		if f.Size == 0 {
			t.Fatalf("file %q has zero size", f.Path)
		}
	}
}

func TestDiff_NewFile(t *testing.T) {
	previous := []FileInfo{
		{Path: "a.conf", ChecksumSHA: "abc123"},
	}
	current := []FileInfo{
		{Path: "a.conf", ChecksumSHA: "abc123"},
		{Path: "b.conf", ChecksumSHA: "def456"},
	}

	changed := Diff(previous, current)
	if len(changed) != 1 {
		t.Fatalf("expected 1 changed file, got %d", len(changed))
	}
	if changed[0].Path != "b.conf" {
		t.Fatalf("expected b.conf, got %s", changed[0].Path)
	}
}

func TestDiff_ModifiedFile(t *testing.T) {
	previous := []FileInfo{
		{Path: "a.conf", ChecksumSHA: "abc123"},
	}
	current := []FileInfo{
		{Path: "a.conf", ChecksumSHA: "xyz789"},
	}

	changed := Diff(previous, current)
	if len(changed) != 1 {
		t.Fatalf("expected 1 changed file, got %d", len(changed))
	}
}

func TestDiff_NoChanges(t *testing.T) {
	files := []FileInfo{
		{Path: "a.conf", ChecksumSHA: "abc123"},
	}

	changed := Diff(files, files)
	if len(changed) != 0 {
		t.Fatalf("expected 0 changes, got %d", len(changed))
	}
}

func TestValidateTarData_Valid(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "a.conf", Size: 3})
	_, _ = tw.Write([]byte("aaa"))
	_ = tw.WriteHeader(&tar.Header{Name: "sub/b.conf", Size: 3})
	_, _ = tw.Write([]byte("bbb"))
	_ = tw.Close()

	if err := ValidateTarData(buf.Bytes()); err != nil {
		t.Fatalf("ValidateTarData: %v", err)
	}
}

func TestValidateTarData_PathTraversal(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	_ = tw.WriteHeader(&tar.Header{Name: "../../etc/passwd", Size: 5})
	_, _ = tw.Write([]byte("evil"))
	_ = tw.Close()

	err := ValidateTarData(buf.Bytes())
	if err == nil {
		t.Fatal("expected error for path traversal in tar")
	}
}

func TestDirectory_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	allowedDirs := []string{dir}

	result, err := Directory(dir, allowedDirs)
	if err != nil {
		t.Fatalf("Directory: %v", err)
	}
	if len(result.Files) != 0 {
		t.Fatalf("expected 0 files in empty dir, got %d", len(result.Files))
	}
}
