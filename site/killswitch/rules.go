// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package killswitch

import (
	"github.com/RhythmShahriar/roadie/core"
)

// Profile is the profile name of every kill-switch rule.
const Profile = "killswitch"

// optionRule changes one leaf of one option. Its ID is
// killswitch.options.option_value.<option>.<leaf>: the option name stands
// in for the row, which the ID format has no segment for.
func optionRule(option, leaf string, a core.Action, value, explain string) core.Rule {
	return core.Rule{
		ID:      Profile + ".options.option_value." + option + "." + leaf,
		Profile: Profile,
		Table:   "{prefix}options",
		Column:  "option_value",
		Where:   map[string]string{"option_name": option},
		Path:    leaf,
		Action:  a,
		Value:   value,
		Explain: explain,
	}
}

// jetpackTokens are the Jetpack connection secrets blanked on a copy.
var jetpackTokens = []struct{ option, leaf string }{
	{"jetpack_private_options", "blog_token"},
	{"jetpack_private_options", "user_tokens"},
	{"jetpack_options", "blog_token"},
	{"jetpack_options", "user_tokens"},
}

func wordpressRules(o Options) []core.Rule {
	var out []core.Rule
	if !o.kept("gateways") {
		for _, g := range o.gateways() {
			if g.ModePath != "" {
				out = append(out, optionRule(g.Option, g.ModePath, core.Fixed, g.TestValue, g.Name+" to test mode"))
			}
			for _, p := range g.LiveSecretPaths {
				out = append(out, optionRule(g.Option, p, core.SwapKey, "", g.Name+" live key swapped for a test key or blanked"))
			}
		}
		for _, id := range unknownGateways(o) {
			out = append(out, optionRule(gatewayOption(id), "enabled", core.Fixed, "no", "gateway "+id+" isn't one Roadie knows, so it is disabled"))
		}
	}
	if !o.kept("mail") {
		for _, m := range o.mailers() {
			if m.MailerPath != "" {
				out = append(out, optionRule(m.Option, m.MailerPath, core.Fixed, m.DefaultMailer, m.ID+" to the default PHP mailer, so Echo catches every wp_mail"))
			}
			for _, p := range m.SecretPaths {
				out = append(out, optionRule(m.Option, p, core.Blank, "", m.ID+" credentials blanked"))
			}
		}
	}
	if !o.kept("webhooks") {
		out = append(out,
			core.Rule{ID: Profile + ".wc_webhooks.status", Profile: Profile, Table: "{prefix}wc_webhooks", Column: "status", Action: core.Fixed, Value: "paused", Explain: "WooCommerce webhooks paused"},
			core.Rule{ID: Profile + ".wc_webhooks.secret", Profile: Profile, Table: "{prefix}wc_webhooks", Column: "secret", Action: core.Blank, Explain: "webhook signing secrets blanked"},
		)
	}
	out = append(out,
		core.Rule{ID: Profile + ".automatewoo_queue", Profile: Profile, Table: "{prefix}automatewoo_queue", Action: core.Truncate, Explain: "AutomateWoo's queued workflows from production dropped"},
		core.Rule{ID: Profile + ".automatewoo_queue_meta", Profile: Profile, Table: "{prefix}automatewoo_queue_meta", Action: core.Truncate, Explain: "AutomateWoo's queued workflows from production dropped"},
	)
	for _, j := range jetpackTokens {
		out = append(out, optionRule(j.option, j.leaf, core.Blank, "", "Jetpack connection tokens blanked"))
	}
	if o.setting("action_scheduler") == "cancel" {
		out = append(out, core.Rule{ID: Profile + ".actionscheduler_actions.status", Profile: Profile, Table: "{prefix}actionscheduler_actions", Column: "status",
			Where: map[string]string{"status": "pending"}, Action: core.Fixed, Value: "canceled", Explain: "pending scheduled actions canceled (side_effects.action_scheduler: cancel)"})
	}
	return out
}
