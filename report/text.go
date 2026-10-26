// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// notCoveredItems are what PRD 19.1's Not covered sentence lists, in its
// order; data.not_covered starts with them.
var notCoveredItems = []string{
	"files and uploads (PDF invoices, form uploads, images with location data)",
	"other databases",
	"object caches (Redis, Memcached)",
	"search indexes (Algolia, ElasticPress)",
	"logs on disk outside wp-content",
	"copies held by third parties (Mailchimp lists, Stripe customers)",
	"names and street addresses written in free text that Roadie didn't see in a structured column",
	"encrypted columns",
	"tables you told Roadie to keep",
}

// NotCoveredSentence is PRD 19.1's Not covered sentence, which every
// report format carries.
var NotCoveredSentence = NotCoveredLine(nil)

// NotCoveredLine writes data.not_covered as the Not covered sentence; an
// empty list gives PRD 19.1's.
func NotCoveredLine(items []string) string {
	if len(items) == 0 {
		items = notCoveredItems
	}
	return "Not covered by this report: " + strings.Join(items, ", ") + "."
}

// StillPersonal is the first sentence of the report footer (PRD 19.1).
const StillPersonal = "Pseudonymised data is still personal data under the GDPR (Article 4(5), Recital 26)."

// Footer is PRD 19.1's report footer for a report signed by keyID, as
// lines; an unsigned report says so instead of naming a key. explain
// pseudonymised prints the same text with the example key ID.
func Footer(keyID string) []string {
	lines := []string{
		StillPersonal,
		"This report shows what Roadie's checks found in this copy; it is not a statement that",
	}
	if keyID == "" {
		return append(lines,
			"the copy holds no personal data. This report is not signed (--sign=off), so nothing",
			"shows it is unchanged since Roadie wrote it.")
	}
	return append(lines,
		"the copy holds no personal data. Signed by "+ShortKeyID(keyID)+" (rhythm-roadie report check report.json):",
		"the signature shows who ran Roadie, which version and rules, on which input and that",
		"the report is unchanged since. It doesn't prove the data holds no personal data.")
}

// ViewNote says what every view is: a rendering of the signed JSON.
const ViewNote = "This view is a rendering of report.json; rhythm-roadie report check report.json checks its signature."

// ShortKeyID is a key ID as screens and views show it: "roadie:" and 7
// characters, then "…" (PRD 16.4).
func ShortKeyID(id string) string {
	rest, ok := strings.CutPrefix(id, "roadie:")
	if !ok || len([]rune(rest)) <= 7 {
		return id
	}
	return "roadie:" + string([]rune(rest)[:7]) + "…"
}

// checkLabels name verify's checks as the screen and the reports say
// them (product.md 5.3). A check without a label shows its ID.
var checkLabels = map[string]string{
	core.FindingExactSurvivors:       "No pseudonymised value survives",
	core.FindingPIIEmail:             "No real-looking emails",
	core.FindingPIIPhone:             "No real-looking phones",
	core.FindingPIIName:              "No real-looking names",
	core.FindingPIIAddress:           "No real-looking addresses",
	core.FindingPIIIP:                "No public IP addresses",
	core.FindingPIICard:              "No card numbers",
	core.FindingPIIIBAN:              "No IBANs",
	"keys":                           "No live keys",
	core.FindingURLProduction:        "No production URLs",
	core.FindingURLProductionSiteURL: "Site URL is the copy's",
	"side-effects":                   "Side effects off",
	core.FindingConfigEnvironment:    "WP_ENVIRONMENT_TYPE is staging",
	core.FindingSearchIndexable:      "Kept out of search engines",
	core.FindingUploadsPersonal:      "No personal files in uploads",
	core.FindingUncoveredColumn:      "Every personal-looking column covered",
}

// CheckLabel names a check: "No real-looking emails" for pii.email. A
// check without a label is named by its ID.
func CheckLabel(id string) string {
	if l, ok := checkLabels[id]; ok {
		return l
	}
	return id
}

// effectLabels name the side effects, long and short.
var effectLabels = map[string][2]string{
	core.FindingSideEffectMail:            {"Echo connector, sealed", "Echo seal"},
	core.FindingConfigEnvironment:         {"WP_ENVIRONMENT_TYPE", "WP_ENVIRONMENT_TYPE"},
	core.FindingSideEffectCron:            {"WP-Cron", "cron"},
	core.FindingSideEffectActionScheduler: {"Action Scheduler", "Action Scheduler"},
	core.FindingSideEffectWebhooks:        {"WooCommerce webhooks", "webhooks"},
	core.FindingSideEffectGateways:        {"Gateways", "gateways"},
	core.FindingSideEffectSubscriptions:   {"Subscriptions", "Subscriptions"},
	core.FindingSideEffectJetpack:         {"Jetpack", "Jetpack"},
	core.FindingSideEffectAutomateWoo:     {"AutomateWoo", "AutomateWoo"},
	core.FindingSideEffectQueue:           {"Queue", "queue"},
	core.FindingSideEffectConnectorChange: {"Echo connector", "Echo connector"},
	core.FindingSideEffectNotApplicable:   {"Database only", "database only"},
	core.FindingConfigSalts:               {"Salts and APP_KEY", "salts"},
	core.FindingSearchIndexable:           {"noindex", "noindex"},
}

// EffectLabel names a side effect: the long form for a list ("WP-Cron"),
// the short one for a sentence ("cron"). An unknown ID is named by itself.
func EffectLabel(id string, short bool) string {
	l, ok := effectLabels[id]
	if !ok {
		return id
	}
	if short {
		return l[1]
	}
	return l[0]
}

// KillSwitchLimits is PRD 13.5's list of what the kill switch can't stop,
// for the Side effects section of the screen and the reports.
const KillSwitchLimits = "a server crontab calling wp cron or artisan schedule:run, wp action-scheduler run by hand, queue workers already running, code that opens SMTP sockets or uses fsockopen with credentials Roadie doesn't know, raw cURL without --echo-proxy. Credentials are blanked so those paths fail at the provider. Blocking outbound traffic at the host is the only certainty."
