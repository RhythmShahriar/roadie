// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/RhythmShahriar/roadie/codec/phpser"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/keys"
	"github.com/RhythmShahriar/roadie/pseudo"
	"github.com/RhythmShahriar/roadie/site/wpconfig"
)

// leaves walks a PHP serialized value; tests replace it until the values
// lane lands phpser.
var leaves = phpser.Leaves

// settings decodes a serialized option into leaf path to value. A value
// that isn't serialized PHP has no leaves.
func settings(b []byte) (map[string]string, error) {
	out := map[string]string{}
	err := leaves(b, func(path string, leaf []byte, isKey bool) {
		if !isKey {
			out[path] = string(leaf)
		}
	})
	return out, err
}

// GatewayIDs returns the gateway IDs in a woocommerce_gateway_order
// option, a serialized array of gateway ID to position.
func GatewayIDs(order []byte) ([]string, error) {
	var ids []string
	err := leaves(order, func(path string, _ []byte, isKey bool) {
		if !isKey && path != "" {
			ids = append(ids, path)
		}
	})
	return ids, err
}

func (o Options) gateways() []keys.Gateway {
	if o.Gateways != nil {
		return o.Gateways
	}
	return keys.Gateways()
}

func (o Options) mailers() []keys.Mailer {
	if o.Mailers != nil {
		return o.Mailers
	}
	return keys.Mailers()
}

func (o Options) scan(pairs []core.Pair) []core.KeyHit {
	if o.Keys != nil {
		return o.Keys(pairs)
	}
	return keys.ScanPairs("wp-config.php", pairs)
}

func (o Options) replace(h core.KeyHit, v []byte) ([]byte, string, error) {
	if o.Replace != nil {
		return o.Replace(h, v)
	}
	mode, test := "swap", map[string]string(nil)
	if o.Cfg != nil {
		mode, test = cmp.Or(o.Cfg.Keys.Mode, mode), o.Cfg.Keys.TestKeys
	}
	return keys.Replace(h, v, mode, test)
}

func (o Options) salt() (string, error) {
	f := o.Salt
	if f == nil {
		f = pseudo.Salt
	}
	s := f()
	if s == "" {
		return "", fmt.Errorf("config.salts: %w", core.ErrNotBuilt)
	}
	return s, nil
}

// setting is a side_effects key of the config, "" when unset.
func (o Options) setting(key string) string {
	if o.Cfg == nil {
		return ""
	}
	se := o.Cfg.SideEffects
	switch key {
	case "cron":
		return se.Cron
	case "action_scheduler":
		return se.ActionScheduler
	case "webhooks":
		return se.Webhooks
	case "gateways":
		return se.Gateways
	case "subscriptions":
		return se.Subscriptions
	case "mail":
		return se.Mail
	}
	return ""
}

// kept reports a side effect the config says to leave on.
func (o Options) kept(key string) bool { return o.setting(key) == "keep" }

// echoInstall reports whether the Echo connector is installed: always
// without a config, else per side_effects.echo.install (true by default)
// unless side_effects.mail is keep.
func (o Options) echoInstall() bool {
	return o.Cfg == nil || o.Cfg.SideEffects.Echo.Install && !o.kept("mail")
}

// readConfig reads and parses wp-config.php.
func readConfig(ctx context.Context, s core.Site) (*wpconfig.File, error) {
	b, err := s.ReadFile(ctx, "wp-config.php")
	if err != nil {
		return nil, err
	}
	return wpconfig.Parse(b)
}

// notBuilt reports a dependency another lane hasn't landed yet.
func notBuilt(err error) bool { return errors.Is(err, core.ErrNotBuilt) }
