// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// chownLog gives the log to owner ("uid:gid"), or leaves it when owner is
// empty.
func chownLog(path, owner string) error {
	if owner == "" {
		return nil
	}
	u, g, ok := strings.Cut(owner, ":")
	uid, err1 := strconv.Atoi(u)
	gid, err2 := strconv.Atoi(g)
	if !ok || err1 != nil || err2 != nil {
		return fmt.Errorf("-owner %q: want uid:gid", owner)
	}
	return os.Chown(path, uid, gid)
}
