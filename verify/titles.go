// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"strconv"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// titles are the one-line titles of the findings verify itself makes.
var titles = map[string]string{
	core.FindingExactSurvivors:           "A production value Roadie replaced survived in the copy",
	core.FindingPIIEmail:                 "Real-looking email addresses",
	core.FindingPIIEmailRole:             "Role mailboxes (noreply, postmaster and the like)",
	core.FindingPIIPhone:                 "Real-looking phone numbers",
	core.FindingPIIName:                  "Real-looking names",
	core.FindingPIIAddress:               "Real-looking street addresses",
	core.FindingPIIIP:                    "Public IP addresses",
	core.FindingPIICard:                  "Payment card numbers",
	core.FindingPIIIBAN:                  "IBANs",
	core.FindingDataEncrypted:            "Encrypted values no scanner can read (Laravel Crypt)",
	core.FindingKeyLiveMode:              "A payment gateway is in live mode",
	core.FindingKeyGeneric:               "A secret-looking setting",
	core.FindingKeyUnknownEnv:            "A provider key Roadie can't tell live from test",
	core.FindingURLProduction:            "Production URLs",
	core.FindingURLProductionSiteURL:     "The site URL is still production's",
	core.FindingConfigPointsAtProduction: "Config points at production",
	core.FindingConfigSalts:              "Salts or APP_KEY are the same as production's",
	core.FindingUploadsPersonal:          "Files that hold personal data",
}

// fixes say what to do about each finding.
var fixes = map[string]string{
	core.FindingExactSurvivors:           "Add a rule for the column (rhythm-roadie explain exact.survivors), then copy again.",
	core.FindingPIIEmail:                 "Cover the column with a rule, or list the address in allow_emails if it may stay real.",
	core.FindingPIIPhone:                 "Cover the column with a rule, or list the number in allow_phones if it may stay real.",
	core.FindingPIIName:                  "Cover the column with a name rule.",
	core.FindingPIIAddress:               "Cover the column with an address rule.",
	core.FindingPIIIP:                    "Cover the column with an ip rule.",
	core.FindingPIICard:                  "Blank the column; a staging copy never needs card numbers.",
	core.FindingPIIIBAN:                  "Blank or fake the column.",
	core.FindingKeyLiveMode:              "Switch the gateway to test or sandbox mode (rhythm-roadie prepare --only side-effects).",
	core.FindingKeyGeneric:               "Blank the setting, or add it to keys.ignore if it is no secret.",
	core.FindingKeyUnknownEnv:            "Check whether the key is live; blank it or give a test key in keys.test_keys.",
	core.FindingURLProduction:            "Copy again with --url so production URLs are rewritten.",
	core.FindingURLProductionSiteURL:     "Copy again with --url, or set siteurl and home to the staging URL.",
	core.FindingConfigPointsAtProduction: "Point the staging config at its own database, cache and storage.",
	core.FindingConfigSalts:              "Run rhythm-roadie prepare to regenerate the salts and APP_KEY.",
	core.FindingUploadsPersonal:          "Delete the files from the staging copy; Roadie never reads or copies them.",
}

// providerNames are the display names of key providers.
var providerNames = map[string]string{
	"stripe": "Stripe", "stripe-publishable": "Stripe publishable", "stripe-webhook": "Stripe webhook",
	"paypal": "PayPal", "braintree": "Braintree", "square": "Square", "woopayments": "WooPayments",
	"mollie": "Mollie", "razorpay": "Razorpay", "adyen": "Adyen", "klarna": "Klarna",
	"authorize-net": "Authorize.net", "flutterwave": "Flutterwave", "woocommerce-rest": "WooCommerce REST",
	"sendgrid": "SendGrid", "mailgun": "Mailgun", "postmark": "Postmark", "ses": "Amazon SES",
	"brevo": "Brevo", "mailchimp": "Mailchimp", "mandrill": "Mandrill", "resend": "Resend",
	"sparkpost": "SparkPost", "mailersend": "MailerSend", "smtp": "SMTP", "twilio": "Twilio",
	"jetpack": "Jetpack", "aws": "AWS",
}

// providerName returns a provider's display name.
func providerName(p string) string {
	if n, ok := providerNames[p]; ok {
		return n
	}
	return p
}

// keyTitle is the title of a key finding: "A live Stripe key".
func keyTitle(h core.KeyHit) string {
	switch {
	case h.ID == core.FindingKeyGeneric:
		return titles[core.FindingKeyGeneric]
	case h.Env == "unknown":
		return "A " + providerName(h.Provider) + " key Roadie can't tell live from test"
	}
	return "A live " + providerName(h.Provider) + " key"
}

// keyManualMessage is the key-manual sentence of PRD 17 for a live key in
// a code file.
func keyManualMessage(h core.KeyHit) string {
	return h.ID + ": " + h.At.File + " line " + strconv.Itoa(h.At.Line) + " holds a live " + providerName(h.Provider) +
		" key. Roadie only edits the database, wp-config.php and .env. Move it into wp-config.php or blank it by hand, then run verify."
}

// skipNames maps each --skip name to the finding IDs it leaves out.
var skipNames = map[string][]string{
	"emails":       {core.FindingPIIEmail, core.FindingPIIEmailRole},
	"phones":       {core.FindingPIIPhone},
	"names":        {core.FindingPIIName},
	"addresses":    {core.FindingPIIAddress},
	"ips":          {core.FindingPIIIP},
	"cards":        {core.FindingPIICard, core.FindingPIIIBAN},
	"keys":         {"key."},
	"urls":         {"url."},
	"side-effects": {"side-effect."},
	"config":       {"config."},
	"uploads":      {core.FindingUploadsPersonal},
}

// skipper says which checks a run leaves out.
type skipper map[string]bool

func newSkipper(skip []string) skipper {
	s := skipper{}
	for _, n := range skip {
		s[strings.ToLower(strings.TrimSpace(n))] = true
	}
	return s
}

// skips reports whether id is left out.
func (s skipper) skips(id string) bool {
	for name, ids := range skipNames {
		if !s[name] {
			continue
		}
		for _, x := range ids {
			if id == x || strings.HasSuffix(x, ".") && strings.HasPrefix(id, x) {
				return true
			}
		}
	}
	return false
}
