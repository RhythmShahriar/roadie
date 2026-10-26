// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package killswitch turns off what a staging copy could do to the real
// world: mail, cron, Action Scheduler, webhooks, payment gateways,
// subscriptions and the rest of PRD 13. It also checks they stay off.
//
// The exported names in this file are the frozen cross-lane API; the site
// lane replaces the bodies, which return zero values until then.
package killswitch

import (
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/keys"
	"github.com/RhythmShahriar/roadie/roadiecfg"
)

// Options configures the kill switch. Keys, Replace, Gateways and Mailers
// default to the keys package; tests pass fixtures.
type Options struct {
	EchoURL    string
	EchoProxy  string
	URL        string
	Production []string
	KeepAppKey bool
	Noindex    bool
	Cfg        *roadiecfg.Config
	Keys       func([]core.Pair) []core.KeyHit                           // nil: keys.ScanPairs
	Replace    func(h core.KeyHit, value []byte) ([]byte, string, error) // nil: keys.Replace with Cfg.Keys
	Gateways   []keys.Gateway                                            // nil: keys.Gateways()
	Mailers    []keys.Mailer                                             // nil: keys.Mailers()
}

// Steps returns the kill-switch steps for the stack.
func Steps(stack core.Stack, o Options) []core.Step {
	return nil
}

// DataRules returns the option-level changes made in the data stream.
func DataRules(stack core.Stack, o Options) []core.Rule {
	return nil
}

// MuPlugin returns roadie-staging.php.
func MuPlugin(o Options) []byte {
	return nil
}
