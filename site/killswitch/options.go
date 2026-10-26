// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

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

// GatewayOptionsMatch accepts the option names SourceGatewayIDs reads:
// woocommerce_gateway_order and every woocommerce_<id>_settings. The
// engine can use it to pick the rows from the source's options table.
func GatewayOptionsMatch(name string) bool {
	return name == "woocommerce_gateway_order" || strings.HasPrefix(name, "woocommerce_") && strings.HasSuffix(name, "_settings") && len(name) > len("woocommerce__settings")
}

// SourceGatewayIDs returns the WooCommerce gateway IDs among the source's
// options, option name to raw value: the keys of woocommerce_gateway_order
// first, then every woocommerce_<id>_settings option shaped like a
// gateway (see gatewayShaped), sorted. WooCommerce writes
// woocommerce_gateway_order only once someone saves the gateway order, so
// a store without it still has its gateways found. The result fills
// Options.GatewayIDs.
func SourceGatewayIDs(opts map[string][]byte) []string {
	var out []string
	if raw, ok := opts["woocommerce_gateway_order"]; ok {
		ids, _ := GatewayIDs(raw)
		for _, id := range ids {
			if !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	var scanned []string
	for name, raw := range opts {
		if !GatewayOptionsMatch(name) || name == "woocommerce_gateway_order" {
			continue
		}
		id := strings.TrimSuffix(strings.TrimPrefix(name, "woocommerce_"), "_settings")
		if slices.Contains(out, id) || !gatewayShaped(id, raw) {
			continue
		}
		scanned = append(scanned, id)
	}
	slices.Sort(scanned)
	return append(out, scanned...)
}

// notGatewayIDs are WooCommerce settings with an enabled flag that are not
// payment gateways: the legacy shipping methods.
var notGatewayIDs = map[string]bool{
	"flat_rate": true, "free_shipping": true, "local_pickup": true, "local_delivery": true, "international_delivery": true,
	"legacy_flat_rate": true, "legacy_free_shipping": true, "legacy_local_pickup": true, "legacy_local_delivery": true, "legacy_international_delivery": true,
}

// emailKeys mark a WooCommerce email's settings (new_order, customer_*
// and the ones extensions add), which also carry an enabled flag.
var emailKeys = []string{"subject", "heading", "email_type", "recipient", "additional_content"}

// gatewayKeys are settings keys a payment gateway has and a plain
// integration rarely does. A key that contains one of gatewayKeyParts
// counts too: API keys, secrets, tokens and merchant IDs.
var (
	gatewayKeys     = []string{"title", "description", "instructions", "testmode", "test_mode", "sandbox", "sandbox_on", "mode", "environment", "live_mode"}
	gatewayKeyParts = []string{"key", "secret", "token", "merchant", "client_id", "api"}
)

// gatewayShaped reports a woocommerce_<id>_settings value that looks like
// a payment gateway: it has an enabled flag and a gateway-shaped key. It
// is neither an email nor a shipping method (legacy or one instance's
// woocommerce_<method>_<n>_settings).
func gatewayShaped(id string, raw []byte) bool {
	if notGatewayIDs[id] || strings.HasPrefix(id, "customer_") {
		return false
	}
	if i := strings.LastIndexByte(id, '_'); i >= 0 && i < len(id)-1 && strings.Trim(id[i+1:], "0123456789") == "" {
		return false
	}
	v, err := settings(raw)
	if err != nil {
		return false
	}
	if _, ok := v["enabled"]; !ok {
		return false
	}
	shaped := false
	for k := range v {
		if slices.Contains(emailKeys, k) {
			return false
		}
		if slices.Contains(gatewayKeys, k) {
			shaped = true
			continue
		}
		for _, part := range gatewayKeyParts {
			if strings.Contains(k, part) {
				shaped = true
			}
		}
	}
	return shaped
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
	return o.scanIn("wp-config.php", pairs)
}

// scanIn finds the keys among the pairs of file.
func (o Options) scanIn(file string, pairs []core.Pair) []core.KeyHit {
	if o.Keys != nil {
		return o.Keys(pairs)
	}
	return keys.ScanPairs(file, pairs)
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
