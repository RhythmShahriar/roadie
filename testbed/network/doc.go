// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package network holds the testbed's seal check, TestNetworkIsSealed
// (build tag testbed): it copies the probe (./probe) into every running
// testbed container and fails when any of them can resolve or reach a
// public address. Only the sinkhole may answer.
//
//	make -C testbed up
//	go test -tags testbed ./testbed/network/
package network
