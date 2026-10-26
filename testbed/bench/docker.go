// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// project is the testbed's Compose project; a service's container is
// <project>-<service>-1.
const project = "roadie-testbed"

// sealedNet is the Compose network every testbed container sits on.
const sealedNet = project + "_sealed"

// container returns the container name of a Compose service.
func container(service string) string { return project + "-" + service + "-1" }

// Execer runs a command in a container. The drill and the harness go
// through it, so tests can record what would run without Docker.
type Execer interface {
	// Exec runs args in container as user ("" for the image's default)
	// with stdin; it returns stdout. A non-zero exit is an *ExitError.
	Exec(ctx context.Context, container, user string, env []string, stdin io.Reader, args ...string) ([]byte, error)
}

// ExitError is a command in a container that exited non-zero. Stderr is
// kept for the harness's own log; it is never put in results.json, where a
// value could leak.
type ExitError struct {
	Code   int
	Stderr string
}

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// dockerExec runs commands with docker exec.
type dockerExec struct{}

// Exec implements Execer with "docker exec". Environment values reach
// docker as -e NAME (the value comes from docker's own environment), so
// no secret is ever on a command line another user can read.
func (dockerExec) Exec(ctx context.Context, name, user string, env []string, stdin io.Reader, args ...string) ([]byte, error) {
	argv := []string{"exec"}
	if stdin != nil {
		argv = append(argv, "-i")
	}
	if user != "" {
		argv = append(argv, "-u", user)
	}
	var extra []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		argv = append(argv, "-e", k)
		extra = append(extra, kv)
	}
	argv = append(argv, name)
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, "docker", argv...) // #nosec G204 -- the harness's own fixed commands
	cmd.Env = append(cmd.Environ(), extra...)
	cmd.Stdin = stdin
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok { //nolint:errorlint // exec returns it unwrapped
			return out.Bytes(), &ExitError{Code: ee.ExitCode(), Stderr: errb.String()}
		}
		return out.Bytes(), err
	}
	return out.Bytes(), nil
}

// docker runs a docker command on this machine (not in a container).
func docker(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "docker", args...) // #nosec G204 -- the harness's own fixed commands
	cmd.Stdin = stdin
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.Bytes(), fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}
