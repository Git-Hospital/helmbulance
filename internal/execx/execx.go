// Package execx runs external commands (helm, kind, kubectl) and keeps a transcript of them, so a
// report can show exactly what was run. Values passed with --set are masked in the transcript.
package execx

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Command is one executed command as it appears in a report.
type Command struct {
	Line     string        `json:"line"`
	Duration time.Duration `json:"duration"`
	ExitCode int           `json:"exitCode"`
}

// Runner executes commands with a fixed environment and records each one.
type Runner struct {
	Env []string

	mu         sync.Mutex
	transcript []Command
}

// Run executes name with args and returns its stdout. On failure the error includes stderr.
func (r *Runner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = r.Env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	rec := Command{Line: Mask(name, args), Duration: time.Since(start).Round(time.Millisecond)}
	if cmd.ProcessState != nil {
		rec.ExitCode = cmd.ProcessState.ExitCode()
	}
	r.mu.Lock()
	r.transcript = append(r.transcript, rec)
	r.mu.Unlock()

	if err != nil {
		return stdout.Bytes(), fmt.Errorf("%s: %w: %s", rec.Line, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// Transcript returns the commands run so far.
func (r *Runner) Transcript() []Command {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Command(nil), r.transcript...)
}

// Mask renders a command line with the values of --set style flags replaced, because they often
// carry passwords.
func Mask(name string, args []string) string {
	out := []string{name}
	maskNext := false
	for _, a := range args {
		switch {
		case maskNext:
			out = append(out, maskAssignments(a))
			maskNext = false
		case isSetFlag(a):
			out = append(out, a)
			maskNext = true
		case strings.HasPrefix(a, "--set") && strings.Contains(a, "="):
			flag, value, _ := strings.Cut(a, "=")
			out = append(out, flag+"="+maskAssignments(value))
		default:
			out = append(out, a)
		}
	}
	return strings.Join(out, " ")
}

func isSetFlag(a string) bool {
	return a == "--set" || a == "--set-string" || a == "--set-file" || a == "--set-json" || a == "--set-literal"
}

// maskAssignments turns "a=1,b.c=2" into "a=***,b.c=***".
func maskAssignments(s string) string {
	parts := strings.Split(s, ",")
	for i, p := range parts {
		if key, _, ok := strings.Cut(p, "="); ok {
			parts[i] = key + "=***"
		}
	}
	return strings.Join(parts, ",")
}
