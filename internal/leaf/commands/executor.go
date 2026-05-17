package commands

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	// MaxOutputBytes is the maximum combined stdout+stderr output.
	MaxOutputBytes = 1 * 1024 * 1024 // 1 MB

	// DefaultTimeout is the default command timeout.
	DefaultTimeout = 30 * time.Second
)

// Result holds the output of a command execution.
type Result struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Timeout  bool   `json:"timeout"`
}

// Executor runs whitelisted commands with strict constraints.
type Executor struct {
	allowedDirs []string
	timeout     time.Duration
}

// NewExecutor creates a new command executor.
func NewExecutor(allowedDirs []string, timeout time.Duration) *Executor {
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	return &Executor{
		allowedDirs: allowedDirs,
		timeout:     timeout,
	}
}

// GetTimeout returns the default command timeout.
func (e *Executor) GetTimeout() time.Duration {
	return e.timeout
}

// commandDef defines how to build a safe command invocation.
type commandDef struct {
	binary  string // actual binary to execute
	fixArgs func([]string) ([]string, error)
}

// commandDefs maps command names to their safe definitions.
// Each definition specifies the actual binary and optional arg sanitization.
var commandDefs = map[string]commandDef{
	"grep": {
		binary: "grep",
		fixArgs: func(args []string) ([]string, error) {
			return filterArgs(args, allowedGrepFlags)
		},
	},
	"awk": {
		binary: "awk",
		fixArgs: func(args []string) ([]string, error) {
			return validateAwkArgs(args)
		},
	},
	"find": {
		binary: "find",
		fixArgs: func(args []string) ([]string, error) {
			return filterArgs(args, allowedFindFlags)
		},
	},
	"cat": {binary: "cat"},
	"tail": {
		binary: "tail",
		fixArgs: func(args []string) ([]string, error) {
			return filterArgs(args, allowedTailFlags)
		},
	},
	"systemctl": {
		binary: "systemctl",
		fixArgs: func(args []string) ([]string, error) {
			return validateSystemctlArgs(args)
		},
	},
	"journalctl": {
		binary: "journalctl",
		fixArgs: func(args []string) ([]string, error) {
			return filterArgs(args, allowedJournalctlFlags)
		},
	},
}

// IsAllowed checks if a command name is in the whitelist.
func IsAllowed(name string) bool {
	_, ok := commandDefs[name]
	return ok
}

// Execute runs a whitelisted command with the given arguments.
// The command runs directly (no shell) with a timeout and output limits.
// fileArgs indices indicate which args are file paths that need validation.
func (e *Executor) Execute(ctx context.Context, command string, args []string, workingDir string) (*Result, error) {
	def, ok := commandDefs[command]
	if !ok {
		return nil, fmt.Errorf("command %q is not allowed", command)
	}

	// Validate working directory
	dir, err := ValidateWorkingDir(workingDir, e.allowedDirs)
	if err != nil {
		return nil, fmt.Errorf("invalid working directory: %w", err)
	}

	// Sanitize arguments
	if def.fixArgs != nil {
		args, err = def.fixArgs(args)
		if err != nil {
			return nil, fmt.Errorf("invalid arguments: %w", err)
		}
	}

	// Look up the binary path
	binaryPath, err := exec.LookPath(def.binary)
	if err != nil {
		return nil, fmt.Errorf("binary %q not found: %w", def.binary, err)
	}

	// Apply timeout
	timeout := e.timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binaryPath, args...)
	cmd.Dir = dir

	// Capture output with size limits
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()

	result := &Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	// Truncate output if too large
	if len(result.Stdout) > MaxOutputBytes {
		result.Stdout = result.Stdout[:MaxOutputBytes] + "\n... (truncated)"
	}
	if len(result.Stderr) > MaxOutputBytes {
		result.Stderr = result.Stderr[:MaxOutputBytes] + "\n... (truncated)"
	}

	if ctx.Err() == context.DeadlineExceeded {
		result.Timeout = true
		result.ExitCode = -1
		return result, nil
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("command execution failed: %w", err)
		}
	}

	return result, nil
}

// --- Flag whitelists ---

var allowedGrepFlags = map[string]bool{
	"-r": true, "-R": true, "-i": true, "-n": true, "-c": true,
	"-v": true, "-l": true, "-w": true, "-x": true, "-E": true,
	"-F": true, "-H": true, "-h": true, "-q": true, "-s": true,
	"-o": true, "-A": true, "-B": true, "-C": true, "-e": true,
	"-f": true, "-m": true, "--color": true, "--include": true,
	"--exclude": true, "--exclude-dir": true,
}

var allowedFindFlags = map[string]bool{
	"-name": true, "-iname": true, "-type": true, "-size": true,
	"-mtime": true, "-maxdepth": true, "-mindepth": true, "-path": true,
	"-ipath": true, "-regex": true, "-iregex": true, "-empty": true,
	"-newer": true, "-not": true, "-and": true, "-or": true,
	"-print": true, "-printf": true, "-user": true, "-group": true,
	"-perm": true, "-executable": true, "-readable": true, "-writable": true,
}

var allowedTailFlags = map[string]bool{
	"-n": true, "-f": true, "-q": true, "-v": true, "-c": true,
	"--pid": true, "-F": true, "--retry": true, "-s": true,
	"--follow": true,
}

var allowedJournalctlFlags = map[string]bool{
	"-u": true, "-n": true, "--since": true, "--until": true,
	"--no-pager": true, "-f": true, "-e": true, "-b": true,
	"-k": true, "-p": true, "--utc": true, "--output": true,
	"-o": true, "--grep": true, "-g": true, "--case-sensitive": true,
	"--catalog": true, "-x": true, "-r": true, "--disk-usage": true,
	"--vacuum-size": true, "--vacuum-time": true,
}

// filterArgs removes any args that look like flags but aren't whitelisted.
// Non-flag args (those not starting with -) are passed through.
func filterArgs(args []string, allowed map[string]bool) ([]string, error) {
	var filtered []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			// Extract the flag name (handle --flag=value and -f value)
			flagName := arg
			if idx := strings.Index(arg, "="); idx >= 0 {
				flagName = arg[:idx]
			}
			if !allowed[flagName] {
				return nil, fmt.Errorf("flag %q is not allowed", flagName)
			}
			filtered = append(filtered, arg)
			// If the flag takes a value (next arg doesn't start with -), include it
			if idx := strings.Index(arg, "="); idx < 0 && !strings.Contains(flagName, "=") {
				// Check if this flag takes a value by looking at the next arg
				if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
					// Heuristic: flags like -A, -B, -C, -e, -f, -m, -n, -c, -s take values
					takesValue := isFlagWithDefaultValue(flagName, allowed)
					if takesValue {
						i++
						filtered = append(filtered, args[i])
					}
				}
			}
		} else {
			filtered = append(filtered, arg)
		}
	}
	return filtered, nil
}

// isFlagWithDefaultValue is a heuristic for flags that take a separate value arg.
func isFlagWithDefaultValue(flag string, allowed map[string]bool) bool {
	valueFlags := map[string]bool{
		"-A": true, "-B": true, "-C": true, "-e": true, "-f": true,
		"-m": true, "-n": true, "-c": true, "-s": true, "-p": true,
		"-u": true, "-b": true, "-o": true, "-g": true,
		"--include": true, "--exclude": true, "--exclude-dir": true,
		"--since": true, "--until": true, "--output": true,
		"--pid": true, "--vacuum-size": true, "--vacuum-time": true,
		"-name": true, "-iname": true, "-type": true, "-size": true,
		"-mtime": true, "-maxdepth": true, "-mindepth": true, "-path": true,
		"-ipath": true, "-regex": true, "-iregex": true, "-newer": true,
		"-user": true, "-group": true, "-perm": true, "-printf": true,
		"--grep": true, "--case-sensitive": true,
	}
	return valueFlags[flag]
}

// validateAwkArgs ensures the awk program doesn't contain dangerous calls.
func validateAwkArgs(args []string) ([]string, error) {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if strings.Contains(lower, "system(") ||
			strings.Contains(lower, "system (") ||
			strings.Contains(lower, "| ") ||
			strings.Contains(lower, "cmd |") ||
			strings.Contains(lower, "coproc") {
			return nil, fmt.Errorf("awk program contains forbidden function (system/pipe/coproc)")
		}
	}
	return args, nil
}

// validateSystemctlArgs ensures only the status subcommand is used.
func validateSystemctlArgs(args []string) ([]string, error) {
	if len(args) == 0 {
		return nil, fmt.Errorf("systemctl requires at least one argument")
	}
	subcmd := args[0]
	if subcmd != "status" {
		return nil, fmt.Errorf("only 'systemctl status' is allowed, got %q", subcmd)
	}
	return args, nil
}
