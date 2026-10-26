// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build testbed

package network

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Project is the testbed's Compose project name.
const project = "roadie-testbed"

// The sealed network's fixed addresses (docker-compose.yml).
var (
	sinkholeIP = envOr("TESTBED_SINKHOLE_IP", "172.31.120.53")
	stubsIP    = envOr("TESTBED_STUBS_IP", "172.31.120.54")
)

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func containers(t *testing.T) []string {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not on PATH")
	}
	out, err := exec.Command("docker", "ps", "--filter", "label=com.docker.compose.project="+project, "--format", "{{.Names}}").Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	names := strings.Fields(string(out))
	if len(names) == 0 {
		if os.Getenv("ROADIE_TESTBED_REQUIRED") == "1" {
			t.Fatal("the testbed is not running (make -C testbed up)")
		}
		t.Skip("the testbed is not running (make -C testbed up)")
	}
	return names
}

func buildProbe(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "roadie-probe")
	cmd := exec.Command("go", "build", "-o", bin, "./probe")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH, "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the probe: %v\n%s", err, out)
	}
	return bin
}

// sinkholeTCP reads runtime/log/sinkhole.jsonl and returns the clients that connected.
func sinkholeTCP(t *testing.T) map[string]bool {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "runtime", "log", "sinkhole.jsonl"))
	if err != nil {
		t.Fatalf("the sinkhole log: %v", err)
	}
	defer f.Close()
	from := map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var r struct{ Kind, From string }
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.Kind == "tcp" {
			from[r.From] = true
		}
	}
	return from
}

func containerIPs(t *testing.T, name string) []string {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "--format", "{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}", name).Output()
	if err != nil {
		t.Fatalf("inspect %s: %v", name, err)
	}
	return strings.Fields(string(out))
}

func TestNetworkIsSealed(t *testing.T) {
	names := containers(t)
	probe := buildProbe(t)
	for _, c := range names {
		t.Run(strings.TrimPrefix(c, project+"-"), func(t *testing.T) {
			if out, err := exec.Command("docker", "cp", probe, c+":/roadie-probe").CombinedOutput(); err != nil {
				t.Fatalf("docker cp: %v %s", err, out)
			}
			defer func() { _ = exec.Command("docker", "exec", "-u", "0", c, "rm", "-f", "/roadie-probe").Run() }()
			var stdout, stderr bytes.Buffer
			cmd := exec.Command("docker", "exec", "-u", "0", c, "/roadie-probe")
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("probe: %v %s", err, stderr.String())
			}
			var r Result
			if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
				t.Fatalf("probe output %q: %v", stdout.String(), err)
			}
			for _, p := range Problems(r, sinkholeIP, stubsIP) {
				t.Error(p)
			}
		})
	}
	// Every container that dialed a public name reached the sinkhole,
	// which recorded it.
	time.Sleep(3 * time.Second)
	seen := sinkholeTCP(t)
	for _, c := range names {
		if strings.Contains(c, "sinkhole") {
			continue
		}
		hit := false
		for _, ip := range containerIPs(t, c) {
			hit = hit || seen[ip]
		}
		if !hit {
			t.Errorf("%s: the sinkhole recorded no connection from it", c)
		}
	}
}
