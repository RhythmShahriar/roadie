// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/RhythmShahriar/rhythm/kit/cli"

	"github.com/RhythmShahriar/roadie/core"
)

// Runner runs allowlisted commands on one host through the system ssh,
// so ~/.ssh/config, agents, jump hosts and known_hosts work as usual.
type Runner struct {
	User, Host string // Host as CheckHost accepts it ([::1] for IPv6)
	Port       int    // 0: ssh's default or ~/.ssh/config
	Root       string // the site's folder, for WP-CLI's --path
	WP         string // remote.wp; "" is wp
	// BatchMode adds -o BatchMode=yes so ssh never prompts. Runner adds
	// it by itself when stdin is not a terminal.
	BatchMode bool
	// ReadOnly refuses every target template; it also lets Cat and TestFile
	// reach only wp-config.php and .env (PRD 6.13 source list).
	ReadOnly bool

	mu       sync.Mutex
	commands []string
}

// stdinIsTerminal is replaced in tests.
var stdinIsTerminal = func() bool { return cli.IsTerminal(os.Stdin) }

// Dest is user@host as ssh takes it.
func (r *Runner) Dest() string {
	if r.User == "" {
		return r.Host
	}
	return r.User + "@" + r.Host
}

// Command builds the remote command string for t, checking it against
// the allowlist and the runner's read-only rule.
func (r *Runner) Command(t Template, args ...string) (string, error) {
	if r.ReadOnly && t.TargetOnly() {
		return "", fmt.Errorf("%w: %s runs only on a target; %s is a read-only source", ErrNotAllowed, t, r.Dest())
	}
	if r.ReadOnly && (t == Cat || t == TestFile) {
		if len(args) != 1 || !sourceReadable(args[0]) {
			return "", fmt.Errorf("%w: on a source, %s reads only wp-config.php and .env", ErrNotAllowed, t)
		}
	}
	cmd, err := build(t, r.WP, r.Root, args)
	if err != nil {
		return "", err
	}
	if _, ok := Allowed(cmd); !ok {
		// build only writes allowlisted shapes; this is the backstop.
		return "", fmt.Errorf("%w: %s built a command outside its shape", ErrNotAllowed, t)
	}
	return cmd, nil
}

// sourceReadable is a file a source may cat: wp-config.php or .env.
func sourceReadable(p string) bool {
	base := p[strings.LastIndexByte(p, '/')+1:]
	return base == "wp-config.php" || base == ".env"
}

// Argv is the full ssh command line for a remote command:
// ssh [-p port] [-o BatchMode=yes] -- dest cmd. It never holds
// StrictHostKeyChecking or UserKnownHostsFile.
func (r *Runner) Argv(cmd string) ([]string, error) {
	if err := CheckHost(r.Host); err != nil {
		return nil, err
	}
	if err := CheckUser(r.User); err != nil {
		return nil, err
	}
	argv := []string{"ssh"}
	if r.Port != 0 {
		argv = append(argv, "-p", strconv.Itoa(r.Port))
	}
	if r.BatchMode || !stdinIsTerminal() {
		argv = append(argv, "-o", "BatchMode=yes")
	}
	return append(argv, "--", r.Dest(), cmd), nil
}

// Run runs template t with stdin (nil for none), streaming its output to
// stdout. Failures are *core.Error for ssh, host key and WP-CLI problems
// (PRD 17) and *RunError for a command that ran and failed.
func (r *Runner) Run(ctx context.Context, stdin io.Reader, stdout io.Writer, t Template, args ...string) error {
	cmd, err := r.Command(t, args...)
	if err != nil {
		return err
	}
	argv, err := r.Argv(cmd)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.commands = append(r.commands, cmd)
	r.mu.Unlock()

	c := exec.CommandContext(ctx, "ssh", argv[1:]...) // #nosec G204 -- argv is built from the allowlist; every argument is quoted
	c.Stdin = stdin
	c.Stdout = stdout
	stderr := &tailBuffer{max: 64 << 10}
	c.Stderr = stderr
	runErr := c.Run()
	if t == Backup && bytes.Contains(stderr.Bytes(), []byte(backupFailed)) && runErr == nil {
		runErr = errors.New("wp db export failed")
	}
	if runErr == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return r.mapError(t, stderr.Bytes(), runErr)
}

// Output runs t and returns what it printed.
func (r *Runner) Output(ctx context.Context, stdin io.Reader, t Template, args ...string) ([]byte, error) {
	var out bytes.Buffer
	err := r.Run(ctx, stdin, &out, t, args...)
	return out.Bytes(), err
}

// Commands lists every remote command run so far, for Site.RemoteCommands.
func (r *Runner) Commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.commands...)
}

// RunError is a remote command that ran and failed. Stderr is the tail of
// what it printed, for callers that map known errors (ERROR 1273).
type RunError struct {
	Template Template
	Exit     int
	Stderr   []byte
}

func (e *RunError) Error() string {
	line := lastLine(e.Stderr)
	if line == "" {
		return fmt.Sprintf("remote %s exited %d", e.Template, e.Exit)
	}
	return fmt.Sprintf("remote %s exited %d: %s", e.Template, e.Exit, line)
}

var (
	hostKeyRE    = regexp.MustCompile(`Host key verification failed|No [A-Z0-9]+ host key is known|REMOTE HOST IDENTIFICATION HAS CHANGED`)
	fingerprRE   = regexp.MustCompile(`([A-Z0-9]+) key fingerprint is (SHA256:[A-Za-z0-9+/=]+)`)
	permissionRE = regexp.MustCompile(`Permission denied \(([a-z,-]+)\)`)
	wpMissingRE  = regexp.MustCompile(`(?m)(?:^|[\s:])wp: (?:command )?not found|wp: No such file or directory`)
)

// mapError turns ssh's stderr into the PRD 17 errors. ssh exits 255 for
// its own failures; anything else is the remote command's.
func (r *Runner) mapError(t Template, stderr []byte, err error) error {
	dest := r.Dest()
	s := string(stderr)
	exit := -1
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		exit = ee.ExitCode()
	}
	switch {
	case hostKeyRE.MatchString(s):
		what := "isn't in known_hosts"
		if strings.Contains(s, "REMOTE HOST IDENTIFICATION HAS CHANGED") {
			what = "has a host key that changed since known_hosts recorded it"
		}
		if m := fingerprRE.FindStringSubmatch(s); m != nil {
			what += " (" + m[1] + " " + m[2] + ")"
		}
		return core.Errorf(core.ErrSSHHostKey, 3, "ssh-host-key: %s %s. Connect once with ssh to check and add it; Roadie never skips host key checks.", dest, what)
	case exit == 255 && permissionRE.MatchString(s):
		m := permissionRE.FindStringSubmatch(s)
		return core.Errorf(core.ErrSSH, 3, "ssh: %s refused the key (Permission denied (%s)). Check ssh %s works on its own; Roadie uses your ssh setup and never skips host key checks.", dest, m[1], dest)
	case wpMissingRE.MatchString(s) || exit == 127 && strings.Contains(s, "wp"):
		wp := r.WP
		if wp == "" {
			wp = "wp"
		}
		return core.Errorf(core.ErrWPCLI, 3, "wp-cli: %s isn't on PATH for %s. Install WP-CLI there, set remote.wp in .roadie/config.yaml to its path, or export the database yourself and use --from dump.sql.gz.", wp, dest)
	case exit == 255 || exit < 0:
		why := "ssh failed"
		if line := lastLine(stderr); strings.HasPrefix(line, "ssh:") {
			why = line
		} else if exit < 0 {
			why = err.Error()
		}
		return core.Errorf(core.ErrSSH, 3, "ssh: couldn't run a command on %s (%s). Check ssh %s works on its own; Roadie uses your ssh setup and never skips host key checks.", dest, why, dest)
	}
	return &RunError{Template: t, Exit: exit, Stderr: bytes.Clone(stderr)}
}

// lastLine is the last non-empty stderr line, without control bytes and
// cut to 200 bytes, for an error message.
func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	line := strings.TrimSpace(lines[len(lines)-1])
	line = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, line)
	if len(line) > 200 {
		line = line[:200]
	}
	return line
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) Bytes() []byte { return t.buf }
