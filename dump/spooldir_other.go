// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

//go:build !linux && !darwin

package dump

// tightenSpoolDir does nothing where folder modes aren't Unix ones; the
// spool file is encrypted either way.
func tightenSpoolDir(string) error { return nil }
