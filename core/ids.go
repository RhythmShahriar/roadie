// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import (
	"slices"
	"strings"
)

// Finding IDs (PRD 15.2). Checks and side effects use the same IDs.
const (
	FindingExactSurvivors            = "exact.survivors"
	FindingPIIEmail                  = "pii.email"
	FindingPIIEmailRole              = "pii.email-role"
	FindingPIIPhone                  = "pii.phone"
	FindingPIIName                   = "pii.name"
	FindingPIIAddress                = "pii.address"
	FindingPIIIP                     = "pii.ip"
	FindingPIICard                   = "pii.card"
	FindingPIIIBAN                   = "pii.iban"
	FindingDataUnparseable           = "data.unparseable"
	FindingDataEncrypted             = "data.encrypted"
	FindingUncoveredColumn           = "uncovered-column"
	FindingKeyLiveMode               = "key.live-mode"
	FindingKeyGeneric                = "key.generic"
	FindingKeyUnknownEnv             = "key.unknown-env"
	FindingURLProduction             = "url.production"
	FindingURLProductionSiteURL      = "url.production-siteurl"
	FindingConfigEnvironment         = "config.environment"
	FindingConfigPointsAtProduction  = "config.points-at-production"
	FindingConfigSalts               = "config.salts"
	FindingSideEffectMail            = "side-effect.mail"
	FindingSideEffectConnectorChange = "side-effect.connector-changed"
	FindingSideEffectCron            = "side-effect.cron"
	FindingSideEffectActionScheduler = "side-effect.action-scheduler"
	FindingSideEffectWebhooks        = "side-effect.webhooks"
	FindingSideEffectGateways        = "side-effect.gateways"
	FindingSideEffectSubscriptions   = "side-effect.subscriptions"
	FindingSideEffectAutomateWoo     = "side-effect.automatewoo"
	FindingSideEffectQueue           = "side-effect.queue"
	FindingSideEffectJetpack         = "side-effect.jetpack"
	FindingSideEffectNotApplicable   = "side-effect.not-applicable"
	FindingSearchIndexable           = "search.indexable"
	FindingAuthNone                  = "auth.none"
	FindingUploadsPersonal           = "uploads.personal"
	FindingSoundcheckFailed          = "soundcheck.failed"
	FindingInfoKeptUsers             = "info.kept-users"
	FindingInfoIgnoreExpired         = "info.ignore-expired"
)

// Error codes (PRD 17), as they appear in data.error.code and explain.
const (
	ErrTargetIsProduction    = "target-is-production"
	ErrTargetSaysProduction  = "target-says-production"
	ErrTargetIsSource        = "target-is-source"
	ErrTargetHoldsProduction = "target-holds-production"
	ErrOutputIsInput         = "output-is-input"
	ErrPrepareConfirm        = "prepare-confirm"
	ErrDumpTruncated         = "dump-truncated"
	ErrDumpFormat            = "dump-format"
	ErrDialectMismatch       = "dialect-mismatch"
	ErrDumpRefused           = "dump-refused"
	ErrDumpBomb              = "dump-bomb"
	ErrValueTooLarge         = "value-too-large"
	ErrSiteConfig            = "site-config"
	ErrSSH                   = "ssh"
	ErrSSHHostKey            = "ssh-host-key"
	ErrWPCLI                 = "wp-cli"
	ErrImport                = "import"
	ErrImportMixed           = "import-mixed"
	ErrUncovered             = "uncovered"
	ErrStack                 = "stack"
	ErrURLMap                = "url-map"
	ErrKeyManual             = "key-manual"
	ErrKillSwitch            = "kill-switch"
	ErrTestKeyIsLive         = "test-key-is-live"
	ErrConfig                = "config"
	ErrNothingScanned        = "nothing-scanned"
	ErrLock                  = "lock"
	ErrFakeCollision         = "fake-collision"
	ErrEchoUnreachable       = "echo-unreachable"
	ErrReportEdited          = "report-edited"
	ErrReportUnsigned        = "report-unsigned"
	// ErrDiskFull is the lead's addition to PRD 17 (exit 3, "not enough
	// free disk space: need about <N>, <M> free in <dir>"), which PRD 10.2
	// and testbed scenario r20 need.
	ErrDiskFull = "disk-full"
	// ErrCodeNotBuilt is the code of ErrNotBuilt, which a frozen API stub
	// returns until its lane lands the real code.
	ErrCodeNotBuilt = "not-built"
)

// codeExit is the exit code of every error code.
var codeExit = map[string]int{
	ErrTargetIsProduction: 3, ErrTargetSaysProduction: 3, ErrTargetIsSource: 3,
	ErrTargetHoldsProduction: 3, ErrOutputIsInput: 2, ErrPrepareConfirm: 2,
	ErrDumpTruncated: 3, ErrDumpFormat: 2, ErrDialectMismatch: 2, ErrDumpRefused: 3,
	ErrDumpBomb: 3, ErrValueTooLarge: 3, ErrSiteConfig: 3, ErrSSH: 3, ErrSSHHostKey: 3,
	ErrWPCLI: 3, ErrImport: 3, ErrImportMixed: 3, ErrUncovered: 1, ErrStack: 2,
	ErrURLMap: 2, ErrKeyManual: 1, ErrKillSwitch: 1, ErrTestKeyIsLive: 2, ErrConfig: 2,
	ErrNothingScanned: 3, ErrLock: 3, ErrFakeCollision: 3, ErrEchoUnreachable: 1,
	ErrReportEdited: 1, ErrReportUnsigned: 3, ErrDiskFull: 3, ErrCodeNotBuilt: 3,
}

// CodeExit returns the exit code PRD 17 gives an error code.
func CodeExit(code string) (int, bool) {
	exit, ok := codeExit[code]
	return exit, ok
}

// findingIDs is every finding ID constant above.
var findingIDs = []string{
	FindingExactSurvivors, FindingPIIEmail, FindingPIIEmailRole, FindingPIIPhone,
	FindingPIIName, FindingPIIAddress, FindingPIIIP, FindingPIICard, FindingPIIIBAN,
	FindingDataUnparseable, FindingDataEncrypted, FindingUncoveredColumn,
	FindingKeyLiveMode, FindingKeyGeneric, FindingKeyUnknownEnv, FindingURLProduction,
	FindingURLProductionSiteURL, FindingConfigEnvironment, FindingConfigPointsAtProduction,
	FindingConfigSalts, FindingSideEffectMail, FindingSideEffectConnectorChange,
	FindingSideEffectCron, FindingSideEffectActionScheduler, FindingSideEffectWebhooks,
	FindingSideEffectGateways, FindingSideEffectSubscriptions, FindingSideEffectAutomateWoo,
	FindingSideEffectQueue, FindingSideEffectJetpack, FindingSideEffectNotApplicable,
	FindingSearchIndexable, FindingAuthNone, FindingUploadsPersonal, FindingSoundcheckFailed,
	FindingInfoKeptUsers, FindingInfoIgnoreExpired,
}

// keyProviders are the providers of PRD 12.2 that have a key.<provider>-live
// finding. The keys lane adds a provider here (a pull request to master)
// before it emits its ID.
var keyProviders = []string{
	"stripe", "stripe-publishable", "stripe-webhook", "paypal", "braintree", "square",
	"woopayments", "mollie", "razorpay", "adyen", "klarna", "authorize-net", "flutterwave",
	"woocommerce-rest", "sendgrid", "mailgun", "postmark", "ses", "brevo", "mailchimp",
	"mandrill", "resend", "sparkpost", "mailersend", "smtp", "twilio", "jetpack", "aws",
}

// FindingIDs returns every fixed finding ID (not the key.<provider>-live ones).
func FindingIDs() []string { return slices.Clone(findingIDs) }

// ErrorCodes returns every error code, sorted.
func ErrorCodes() []string {
	out := make([]string, 0, len(codeExit))
	for c := range codeExit {
		out = append(out, c)
	}
	slices.Sort(out)
	return out
}

// KeyProviders returns the providers that have a key.<provider>-live ID.
func KeyProviders() []string { return slices.Clone(keyProviders) }

// KeyLiveID returns the finding ID of a live key of provider, "key.stripe-live".
func KeyLiveID(provider string) string { return "key." + provider + "-live" }

// KnownID reports whether id is a finding ID, a key.<provider>-live ID or
// an error code. Every lane's tests assert it for each ID they emit.
func KnownID(id string) bool {
	if slices.Contains(findingIDs, id) {
		return true
	}
	if _, ok := codeExit[id]; ok {
		return true
	}
	if p, ok := strings.CutPrefix(id, "key."); ok {
		if p, ok = strings.CutSuffix(p, "-live"); ok {
			return slices.Contains(keyProviders, p)
		}
	}
	return false
}
