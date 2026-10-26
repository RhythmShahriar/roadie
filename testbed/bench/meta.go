// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Meta is the run's metadata for docs/benchmark.md (PRD 22.3): when, on
// what and with which versions.
type Meta struct {
	Date          string `json:"date"` // RFC 3339, UTC
	RoadieVersion string `json:"roadie_version"`
	GoVersion     string `json:"go_version"`
	OS            string `json:"os"`
	Arch          string `json:"arch"`
	CPU           string `json:"cpu"`
	Cores         int    `json:"cores"`
	RAMMB         int64  `json:"ram_mb"`
	Disk          string `json:"disk"`
	Docker        string `json:"docker"`
	WallMS        int64  `json:"wall_ms"`
}

// collectMeta reads the machine's facts. Anything it can't read stays
// empty: metadata never fails a run.
func collectMeta(ctx context.Context, bin, work string) Meta {
	m := Meta{Date: time.Now().UTC().Format(time.RFC3339), GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, Cores: runtime.NumCPU()}
	if out, err := exec.CommandContext(ctx, bin, "version", "--json").Output(); err == nil { // #nosec G204 -- the binary under test
		var v struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(out, &v) == nil {
			m.RoadieVersion = v.Version
		}
	}
	m.CPU = procField("/proc/cpuinfo", "model name")
	if kb := procField("/proc/meminfo", "MemTotal"); kb != "" {
		n, _ := strconv.ParseInt(strings.Fields(kb)[0], 10, 64)
		m.RAMMB = n / 1024
	}
	m.Disk = diskType(ctx, work)
	if out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Output(); err == nil {
		m.Docker = strings.TrimSpace(string(out))
	}
	return m
}

// procField returns the value of the first "key: value" line of a /proc
// file.
func procField(path, key string) string {
	f, err := os.Open(path) // #nosec G304 -- a fixed /proc file
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// diskType names the kind of disk the work folder is on (Linux): NVMe
// SSD, SSD or HDD, from the block device's rotational flag.
func diskType(ctx context.Context, dir string) string {
	out, err := exec.CommandContext(ctx, "findmnt", "-no", "SOURCE", "-T", dir).Output() // #nosec G204 -- the harness's own work folder
	if err != nil {
		return ""
	}
	dev := filepath.Base(strings.TrimSpace(string(out)))
	base := blockDevice(dev)
	rot, err := os.ReadFile(filepath.Join("/sys/block", base, "queue", "rotational")) // #nosec G304 -- a /sys file named by the device
	switch {
	case err != nil:
		return ""
	case strings.TrimSpace(string(rot)) == "1":
		return "HDD"
	case strings.HasPrefix(base, "nvme"):
		return "NVMe SSD"
	}
	return "SSD"
}

// blockDevice strips the partition from a device name: nvme0n1p2 is
// nvme0n1, sda1 is sda.
func blockDevice(dev string) string {
	if strings.HasPrefix(dev, "nvme") || strings.HasPrefix(dev, "mmcblk") {
		if i := strings.LastIndex(dev, "p"); i > 0 && i < len(dev)-1 && strings.Trim(dev[i+1:], "0123456789") == "" {
			return dev[:i]
		}
		return dev
	}
	return strings.TrimRight(dev, "0123456789")
}
