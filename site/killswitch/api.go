// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package killswitch turns off what a staging copy could do to the real
// world: mail, cron, Action Scheduler, webhooks, payment gateways,
// subscriptions and the rest of PRD 13. It also checks they stay off.
//
// The exported names in this file are the frozen cross-lane API; new
// names are added, never changed.
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
	// Version is rhythm-roadie's version, for the mu-plugin header.
	Version string
	// Salt makes one wp-config salt; nil is pseudo.Salt. Tests pass a
	// fixed one for golden files.
	Salt func() string
	// GatewayIDs are the WooCommerce gateway IDs the source holds, from
	// SourceGatewayIDs over its woocommerce_gateway_order and
	// woocommerce_<id>_settings options. DataRules disables each one the
	// gateway list doesn't know.
	GatewayIDs []string
	// AppKey makes one Laravel APP_KEY; nil is pseudo.AppKey. Tests pass a
	// fixed one for golden files.
	AppKey func() string
}

// Steps returns the kill-switch steps for the stack, in the order they
// apply. Every step is in phase "files": WordPress option changes are
// DataRules, Laravel's are .env edits. A sql target (a DSN, or a folder
// with neither stack) gets one check-only step that reports
// side-effect.not-applicable. The exposure steps come from the exposure
// package: exposure.AllSteps returns these followed by them.
func Steps(stack core.Stack, o Options) []core.Step {
	switch stack {
	case core.StackWordPress:
		return wordpressSteps(o)
	case core.StackLaravel:
		return laravelSteps(o)
	case core.StackSQL:
		return []core.Step{databaseOnlyStep()}
	}
	return nil
}

// DataRules returns the changes made in the data stream: WordPress option
// leaves, webhooks and queues; Laravel's queued jobs. A sql target has none.
func DataRules(stack core.Stack, o Options) []core.Rule {
	switch stack {
	case core.StackWordPress:
		return wordpressRules(o)
	case core.StackLaravel:
		return laravelRules(o)
	}
	return nil
}

// MuPlugin returns roadie-staging.php: the Roadie mu-plugin that pauses
// Action Scheduler, marks the site a Subscriptions duplicate, puts
// Jetpack offline, blocks wp_mail when the Echo connector is absent, adds
// noindex (when o.Noindex) and shows an admin-bar notice. It does nothing
// on a site whose environment type is production.
func MuPlugin(o Options) []byte {
	return muPlugin(o)
}
