// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/RhythmShahriar/rhythm/kit/cli"
)

// profilesPack is the name of the profile pack in update's data report,
// the name rhythm lock pins (profiles=<version>).
const profilesPack = "profiles"

// offlineUpdate reports whether update must stay off the network:
// --offline, or RHYTHM_OFFLINE set to anything but "" or "0" (as for the
// other commands).
func offlineUpdate(args []string) bool {
	if cli.HasFlag(args, "offline") {
		return true
	}
	v, _ := dep.env(cli.OfflineEnv)
	return v != "" && v != "0"
}

// updateOffline is update without the network: it downloads nothing and
// reports the profile pack Roadie uses now (the cached pack when it is
// newer, else the built-in one) in kit's rhythm-data/v1 shape, so
// rhythm doctor and rhythm lock read an offline machine like any other.
// --pin needs a download, so it is a usage error here.
func updateOffline(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	asJSON := fs.Bool("json", false, "")
	fs.Bool("offline", false, "")
	pin := fs.String("pin", "", "")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "%s update: %v\n", prog(), err)
		return cli.ExitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "%s update: unexpected argument %q\n", prog(), fs.Arg(0))
		return cli.ExitUsage
	}
	if *pin != "" {
		fmt.Fprintf(stderr, "%s update: --pin downloads the pinned pack, so it can't run offline: drop --offline (and unset %s)\n", prog(), cli.OfflineEnv)
		return cli.ExitUsage
	}
	defs, version, err := dep.loadProfiles(context.Background(), true)
	if err != nil {
		fmt.Fprintf(stderr, "%s: update failed: %v\n", prog(), err)
		return cli.ExitUnavailable
	}
	// The digest of the profiles in use: the same pack gives the same
	// digest, whether it came from the cache or the binary.
	b, err := json.Marshal(defs)
	if err != nil {
		fmt.Fprintf(stderr, "%s: update failed: %v\n", prog(), err)
		return cli.ExitUnavailable
	}
	sum := sha256.Sum256(b)
	packs := []cli.DataPack{{Name: profilesPack, Version: version, SHA256: hex.EncodeToString(sum[:])}}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(struct {
			Schema string         `json:"schema"`
			Data   []cli.DataPack `json:"data"`
		}{cli.DataSchema, packs}); err != nil {
			return cli.ExitUnavailable
		}
		return cli.ExitOK
	}
	fmt.Fprintln(stderr, "Offline: nothing was downloaded. This is the profile pack Roadie uses now.")
	for _, p := range packs {
		fmt.Fprintf(stdout, "%s %s\n", p.Name, p.Version)
	}
	return cli.ExitOK
}
