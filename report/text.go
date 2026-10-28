// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"slices"
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
func Footer(keyID string) []string { return footerLines(ShortKeyID(keyID)) }

// footerLines is the footer with the key's short ID as given, so each
// view can escape it for its format.
func footerLines(shortID string) []string {
	lines := []string{
		StillPersonal,
		"This report shows what Roadie's checks found in this copy; it is not a statement that",
	}
	if shortID == "" {
		return append(lines,
			"the copy holds no personal data. This report is not signed (--sign=off), so nothing",
			"shows it is unchanged since Roadie wrote it.")
	}
	return append(lines,
		"the copy holds no personal data. Signed by "+shortID+" (rhythm-roadie report check report.json):",
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
	core.FindingExactSurvivors:           "No pseudonymised value survives",
	core.FindingPIIEmail:                 "No real-looking emails",
	core.FindingPIIPhone:                 "No real-looking phones",
	core.FindingPIIName:                  "No real-looking names",
	core.FindingPIIAddress:               "No real-looking addresses",
	core.FindingPIIIP:                    "No public IP addresses",
	core.FindingPIICard:                  "No card numbers",
	core.FindingPIIIBAN:                  "No IBANs",
	"keys":                               "No live keys",
	core.FindingURLProduction:            "No production URLs",
	core.FindingURLProductionSiteURL:     "Site URL is the copy's",
	"side-effects":                       "Side effects off",
	core.FindingConfigEnvironment:        "The site says it is staging", // WP_ENVIRONMENT_TYPE or APP_ENV
	core.FindingSearchIndexable:          "Kept out of search engines",
	core.FindingUploadsPersonal:          "No personal files in uploads",
	core.FindingUncoveredColumn:          "Every personal-looking column covered",
	core.FindingDataEncrypted:            "Encrypted values listed",
	core.FindingDataUnparseable:          "Every serialized value parsed",
	"key.live":                           "No live keys",
	core.FindingKeyGeneric:               "No secret-looking settings",
	core.FindingKeyLiveMode:              "No gateway in live mode",
	core.FindingKeyUnknownEnv:            "No provider key of unknown mode",
	core.FindingConfigPointsAtProduction: "Nothing points at production",
	core.FindingConfigSalts:              "Salts and APP_KEY are the copy's own",
	core.FindingAuthNone:                 "Asks for a password",
	core.FindingSoundcheckFailed:         "Soundcheck passed",
	core.FindingSideEffectNotApplicable:  "Database only: no files to check",
}

// checkNames name verify's checks without a verdict, for a check that
// has findings: "No real-looking emails" above its own findings would
// read as reassurance.
var checkNames = map[string]string{
	core.FindingExactSurvivors:           "Pseudonymised values",
	core.FindingPIIEmail:                 "Real-looking emails",
	core.FindingPIIPhone:                 "Real-looking phones",
	core.FindingPIIName:                  "Real-looking names",
	core.FindingPIIAddress:               "Real-looking addresses",
	core.FindingPIIIP:                    "Public IP addresses",
	core.FindingPIICard:                  "Card numbers",
	core.FindingPIIIBAN:                  "IBANs",
	"keys":                               "Live keys",
	core.FindingURLProduction:            "Production URLs",
	core.FindingURLProductionSiteURL:     "Site URL",
	"side-effects":                       "Side effects",
	core.FindingConfigEnvironment:        "Environment",
	core.FindingSearchIndexable:          "Search engines",
	core.FindingUploadsPersonal:          "Files in uploads",
	core.FindingUncoveredColumn:          "Personal-looking columns",
	core.FindingDataEncrypted:            "Encrypted values",
	core.FindingDataUnparseable:          "Unparsed serialized values",
	"key.live":                           "Live keys",
	core.FindingKeyGeneric:               "Secret-looking settings",
	core.FindingKeyLiveMode:              "Gateways in live mode",
	core.FindingKeyUnknownEnv:            "Provider keys of unknown mode",
	core.FindingConfigPointsAtProduction: "Points at production",
	core.FindingConfigSalts:              "Salts and APP_KEY",
	core.FindingAuthNone:                 "No password",
	core.FindingSoundcheckFailed:         "Soundcheck",
	core.FindingSideEffectNotApplicable:  "Database only",
}

// CheckTitle names a check as a list shows it with its status: the
// label (a statement such as "No real-looking emails") when it passed
// or was skipped, a plain name ("Real-looking emails") when it warns or
// fails, so the line never says the opposite of its findings. A check
// with neither is named by its ID.
func CheckTitle(id, status string) string {
	if status == "warn" || status == "fail" {
		if n, ok := checkNames[id]; ok {
			return n
		}
		if _, ok := effectLabels[id]; ok && strings.HasPrefix(id, "side-effect.") {
			return EffectLabel(id, false)
		}
		if _, ok := checkLabels[id]; ok {
			return id
		}
	}
	return CheckLabel(id)
}

// CheckLabel names a check: "No real-looking emails" for pii.email, "Off:
// WP-Cron" for side-effect.cron. A check without a label is named by its
// ID.
func CheckLabel(id string) string {
	if l, ok := checkLabels[id]; ok {
		return l
	}
	if _, ok := effectLabels[id]; ok && strings.HasPrefix(id, "side-effect.") {
		return "Off: " + EffectLabel(id, false)
	}
	return id
}

// effectLabels name the side effects, long and short.
var effectLabels = map[string][2]string{
	core.FindingSideEffectMail:            {"Echo connector, sealed", "Echo seal"},
	core.FindingConfigEnvironment:         {"Environment", "environment"}, // WP_ENVIRONMENT_TYPE or APP_ENV
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

// escapeExtras escapes the items of a not_covered list that aren't PRD
// 19.1's own, which are fixed text.
func escapeExtras(items []string, esc func(string) string) []string {
	out := make([]string, 0, len(items))
	for _, s := range items {
		if !slices.Contains(notCoveredItems, s) {
			s = esc(s)
		}
		out = append(out, s)
	}
	return out
}
