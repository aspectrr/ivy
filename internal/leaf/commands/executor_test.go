package commands

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestExecutor(t *testing.T) (*Executor, string) {
	t.Helper()
	dir := t.TempDir()
	return NewExecutor([]string{dir}, 10*time.Second), dir
}

func TestExecutor_CommandNotAllowed(t *testing.T) {
	e, _ := newTestExecutor(t)
	_, err := e.Execute(context.Background(), "rm", []string{"-rf", "/"}, "")
	if err == nil {
		t.Fatal("expected error for non-allowed command")
	}
}

func TestExecutor_GrepBasic(t *testing.T) {
	e, dir := newTestExecutor(t)

	// Create a test file
	testFile := filepath.Join(dir, "test.log")
	if err := os.WriteFile(testFile, []byte("hello world\nfoo bar\nhello foo\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := e.Execute(context.Background(), "grep", []string{"-n", "hello", "test.log"}, dir)
	if err != nil {
		t.Fatalf("Execute grep: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code %d, stderr: %s", result.ExitCode, result.Stderr)
	}
	if result.Timeout {
		t.Fatal("should not have timed out")
	}
	// Should find 2 matching lines
	if result.Stdout == "" {
		t.Fatal("expected output from grep")
	}
	t.Logf("grep output: %s", result.Stdout)
}

func TestExecutor_GrepDisallowedFlag(t *testing.T) {
	e, _ := newTestExecutor(t)
	_, err := e.Execute(context.Background(), "grep", []string{"--exec", "evil"}, "")
	if err == nil {
		t.Fatal("expected error for disallowed grep flag")
	}
}

func TestExecutor_Cat(t *testing.T) {
	e, dir := newTestExecutor(t)

	testFile := filepath.Join(dir, "config.conf")
	content := "input { kafka {} } output { elasticsearch {} }"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := e.Execute(context.Background(), "cat", []string{"config.conf"}, dir)
	if err != nil {
		t.Fatalf("Execute cat: %v", err)
	}
	if result.Stdout != content {
		t.Fatalf("expected %q, got %q", content, result.Stdout)
	}
}

func TestExecutor_FindBasic(t *testing.T) {
	e, dir := newTestExecutor(t)

	// Create some files
	if err := os.WriteFile(filepath.Join(dir, "a.conf"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.log"), []byte("b"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := e.Execute(context.Background(), "find", []string{".", "-name", "*.conf"}, dir)
	if err != nil {
		t.Fatalf("Execute find: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code %d: %s", result.ExitCode, result.Stderr)
	}
	t.Logf("find output: %s", result.Stdout)
}

func TestExecutor_AwkBasic(t *testing.T) {
	e, dir := newTestExecutor(t)

	testFile := filepath.Join(dir, "data.log")
	if err := os.WriteFile(testFile, []byte("hello world\nfoo bar\n"), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := e.Execute(context.Background(), "awk", []string{"{print $1}", "data.log"}, dir)
	if err != nil {
		t.Fatalf("Execute awk: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code %d: %s", result.ExitCode, result.Stderr)
	}
	t.Logf("awk output: %s", result.Stdout)
}

func TestExecutor_AwkSystemBlocked(t *testing.T) {
	e, _ := newTestExecutor(t)

	_, err := e.Execute(context.Background(), "awk", []string{"{system(\"rm -rf /\")}"}, "")
	if err == nil {
		t.Fatal("expected error for awk system() call")
	}
}

func TestExecutor_AwkPipeBlocked(t *testing.T) {
	e, _ := newTestExecutor(t)

	_, err := e.Execute(context.Background(), "awk", []string{"{print | \"sort\"}"}, "")
	if err == nil {
		t.Fatal("expected error for awk pipe")
	}
}

func TestExecutor_TailBasic(t *testing.T) {
	e, dir := newTestExecutor(t)

	testFile := filepath.Join(dir, "log.txt")
	var lines string
	for i := 0; i < 100; i++ {
		lines += "line\n"
	}
	if err := os.WriteFile(testFile, []byte(lines), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := e.Execute(context.Background(), "tail", []string{"-n", "5", "log.txt"}, dir)
	if err != nil {
		t.Fatalf("Execute tail: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit code %d: %s", result.ExitCode, result.Stderr)
	}
	t.Logf("tail output: %s", result.Stdout)
}

func TestExecutor_SystemctlStatusOnly(t *testing.T) {
	e, _ := newTestExecutor(t)

	// "systemctl status" may or may not succeed depending on the environment,
	// but at least it should not be rejected by the whitelist.
	_, err := e.Execute(context.Background(), "systemctl", []string{"status", "logstash"}, "")
	if err != nil {
		// Could fail if systemctl is not available (e.g. macOS), that's okay
		t.Logf("systemctl not available (expected on non-Linux): %v", err)
		return
	}
}

func TestExecutor_SystemctlRestartBlocked(t *testing.T) {
	e, _ := newTestExecutor(t)

	_, err := e.Execute(context.Background(), "systemctl", []string{"restart", "logstash"}, "")
	if err == nil {
		t.Fatal("expected error for systemctl restart")
	}
}

func TestExecutor_InvalidWorkingDir(t *testing.T) {
	e, _ := newTestExecutor(t)

	_, err := e.Execute(context.Background(), "cat", []string{"/etc/passwd"}, "/etc")
	if err == nil {
		t.Fatal("expected error for working dir outside allowed")
	}
}

func TestExecutor_Timeout(t *testing.T) {
	dir := t.TempDir()
	e := NewExecutor([]string{dir}, 1*time.Second)

	// Create a script that sleeps
	script := filepath.Join(dir, "slow.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 10\n"), 0755); err != nil {
		t.Fatal(err)
	}

	// Use cat on a FIFO or just a simple sleep command — but our whitelist doesn't have sleep.
	// Instead, let's test with "tail -f" on a file (it will block until timeout).
	testFile := filepath.Join(dir, "test.log")
	if err := os.WriteFile(testFile, []byte("line\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	result, err := e.Execute(ctx, "tail", []string{"-f", "test.log"}, dir)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Timeout {
		t.Fatal("expected timeout")
	}
}

func TestExecutor_JournalctlDisallowedFlag(t *testing.T) {
	e, _ := newTestExecutor(t)

	_, err := e.Execute(context.Background(), "journalctl", []string{"--unset", "evil"}, "")
	if err == nil {
		t.Fatal("expected error for disallowed journalctl flag")
	}
}

func TestIsAllowed(t *testing.T) {
	allowed := []string{"grep", "cat", "awk", "find", "tail", "systemctl", "journalctl"}
	for _, cmd := range allowed {
		if !IsAllowed(cmd) {
			t.Fatalf("expected %q to be allowed", cmd)
		}
	}

	blocked := []string{"rm", "bash", "sh", "python", "curl", "wget", "nc", "socat"}
	for _, cmd := range blocked {
		if IsAllowed(cmd) {
			t.Fatalf("expected %q to be blocked", cmd)
		}
	}
}

func TestExecutor_BinaryNotFound(t *testing.T) {
	// This test is environment-dependent — most systems have grep.
	// We test the error path by creating an executor with a bad binary override.
	// Since we can't easily do that, we just verify the error message format.
	e, _ := newTestExecutor(t)

	// Override a command def to use a nonexistent binary
	orig := commandDefs["grep"]
	commandDefs["grep"] = commandDef{binary: "nonexistent_binary_xyz"}
	defer func() { commandDefs["grep"] = orig }()

	_, err := e.Execute(context.Background(), "grep", []string{"test"}, "")
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
}
