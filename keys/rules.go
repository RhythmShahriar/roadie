// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keys

import (
	"bytes"
	"net/netip"
	"slices"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/secrets"

	"github.com/RhythmShahriar/roadie/core"
)

// The environments Classify returns.
const (
	EnvLive    = "live"
	EnvTest    = "test"
	EnvUnknown = "unknown"
)

// ProviderGeneric is the Provider of a key.generic hit: a secret kit's
// name rules found that no Roadie rule claims.
const ProviderGeneric = "generic"

// tokenRule finds one provider's key format by its own marker: one of
// marks followed by body, tried at a token start (the byte before is not
// [A-Za-z0-9_]). A mark's env is "live" or "test" when the format says,
// "" when only the settings next to the key can tell. A rule with no
// marks uses whole instead, which matches from the token start.
type tokenRule struct {
	provider string
	sev      core.Severity
	marks    []mark
	body     func(v []byte, i int) int
	whole    func(v []byte, i int) (end int, env string)
	firsts   string // the bytes a whole rule can start with
	// context, when set, must appear in the lower-cased name, row or path
	// for the rule to count, for formats too plain to stand alone.
	context string
}

// mark is a key prefix and the environment it means.
type mark struct {
	text, env string
}

// match tries the rule at i.
func (r *tokenRule) match(v []byte, i int) (int, string) {
	if r.whole != nil {
		return r.whole(v, i)
	}
	for _, m := range r.marks {
		if !bytes.HasPrefix(v[i:], []byte(m.text)) {
			continue
		}
		if end := r.body(v, i+len(m.text)); end > 0 {
			return end, m.env
		}
	}
	return -1, ""
}

func marks(pairs ...string) []mark {
	out := make([]mark, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, mark{pairs[i], pairs[i+1]})
	}
	return out
}

// tokenRules is Roadie's own table for PRD 12.2, specific formats first.
// Formats follow each provider's published key prefixes:
// https://docs.stripe.com/keys, https://docs.mollie.com/reference/authentication,
// https://razorpay.com/docs/api/authentication/, https://developer.squareup.com/docs/build-basics/access-tokens,
// https://developer.paypal.com/braintree/docs/guides/authorization/overview,
// https://developer.flutterwave.com/docs/integration-guides/authentication,
// https://docs.adyen.com/development-resources/api-credentials/,
// https://woocommerce.github.io/woocommerce-rest-api-docs/#authentication.
var tokenRules = []tokenRule{
	{provider: "stripe", sev: core.SeverityCritical, body: alnum(16, 255),
		marks: marks("sk_live_", EnvLive, "rk_live_", EnvLive, "sk_test_", EnvTest, "rk_test_", EnvTest)},
	{provider: "stripe-publishable", sev: core.SeverityHigh, body: alnum(16, 255),
		marks: marks("pk_live_", EnvLive, "pk_test_", EnvTest)},
	{provider: "stripe-webhook", sev: core.SeverityHigh, body: alnum(16, 255), marks: marks("whsec_", "")},
	{provider: "braintree", sev: core.SeverityCritical, body: braintreeBody,
		marks: marks("access_token$production$", EnvLive, "access_token$sandbox$", EnvTest)},
	{provider: "square", sev: core.SeverityCritical, body: chars(22, 128, isTokenByte),
		marks: marks("sandbox-sq0", EnvTest, "sq0idp-", "", "sq0atp-", "", "sq0csp-", "", "EAAA", "")},
	{provider: "razorpay", sev: core.SeverityCritical, body: alnum(14, 40),
		marks: marks("rzp_live_", EnvLive, "rzp_test_", EnvTest)},
	{provider: "mollie", sev: core.SeverityCritical, body: alnum(30, 30),
		marks: marks("live_", EnvLive, "test_", EnvTest)},
	{provider: "flutterwave", sev: core.SeverityCritical, body: flutterwaveBody,
		marks: marks("FLWSECK_TEST-", EnvTest, "FLWSECK-", EnvLive)},
	{provider: "adyen", sev: core.SeverityCritical, context: "adyen", body: chars(60, 400, isAdyenByte), marks: marks("AQE", "")},
	{provider: "woocommerce-rest", sev: core.SeverityHigh, body: lowerHex(40), marks: marks("ck_", EnvLive, "cs_", EnvLive)},
	{provider: "aws", sev: core.SeverityHigh, body: chars(16, 16, isUpperAlnum), marks: marks("AKIA", EnvLive, "ASIA", EnvLive)},
	{provider: "sendgrid", sev: core.SeverityHigh, body: sendgridBody, marks: marks("SG.", EnvLive)},
	{provider: "mailgun", sev: core.SeverityHigh, body: lowerHex(32), marks: marks("key-", EnvLive)},
	{provider: "brevo", sev: core.SeverityHigh, body: chars(70, 90, isTokenByte), marks: marks("xkeysib-", EnvLive, "xsmtpsib-", EnvLive)},
	{provider: "mailersend", sev: core.SeverityHigh, body: alnum(40, 100), marks: marks("mlsn.", EnvLive)},
	{provider: "resend", sev: core.SeverityHigh, body: resendBody, marks: marks("re_", EnvLive)},
	{provider: "mailchimp", sev: core.SeverityHigh, whole: mailchimpKey, firsts: "0123456789abcdef"},
}

// chars matches min to max bytes that ok accepts, ending at a byte it
// doesn't (or the end of v), so a longer run is not cut to fit.
func chars(lo, hi int, ok func(c byte) bool) func(v []byte, i int) int {
	return func(v []byte, i int) int {
		n := 0
		for i+n < len(v) && ok(v[i+n]) {
			n++
			if n > hi {
				return -1
			}
		}
		if n < lo {
			return -1
		}
		return i + n
	}
}

func alnum(lo, hi int) func(v []byte, i int) int { return chars(lo, hi, isAlnum) }

// lowerHex matches exactly n lower-case hex digits not followed by
// another word byte.
func lowerHex(n int) func(v []byte, i int) int {
	return func(v []byte, i int) int {
		end := chars(n, n, func(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' })(v, i)
		if end < 0 || end < len(v) && isWord(v[end]) {
			return -1
		}
		return end
	}
}

// braintreeBody is <16 [0-9a-z]>$<32 hex>, loosely: the merchant part
// and the hex part joined by one '$'.
func braintreeBody(v []byte, i int) int {
	end := chars(8, 40, func(c byte) bool { return isAlnum(c) })(v, i)
	if end < 0 || end >= len(v) || v[end] != '$' {
		return -1
	}
	return chars(16, 64, isAlnum)(v, end+1)
}

// flutterwaveBody is 20 to 64 letters and digits with an optional "-X".
func flutterwaveBody(v []byte, i int) int {
	end := alnum(20, 64)(v, i)
	if end > 0 && end+1 < len(v) && v[end] == '-' && v[end+1] == 'X' && (end+2 == len(v) || !isWord(v[end+2])) {
		return end + 2
	}
	return end
}

// sendgridBody is <16+ chars>.<16+ chars>.
func sendgridBody(v []byte, i int) int {
	isPart := func(c byte) bool { return isAlnum(c) || c == '_' || c == '-' }
	end := chars(16, 64, isPart)(v, i)
	if end < 0 || end >= len(v) || v[end] != '.' {
		return -1
	}
	return chars(16, 128, isPart)(v, end+1)
}

// resendBody is 20 to 64 of [A-Za-z0-9_] holding a digit and a letter,
// so a long snake_case word after "re_" is not a key.
func resendBody(v []byte, i int) int {
	end := chars(20, 64, func(c byte) bool { return isAlnum(c) || c == '_' })(v, i)
	if end < 0 {
		return -1
	}
	digit, upper := false, false
	for _, c := range v[i:end] {
		digit = digit || c >= '0' && c <= '9'
		upper = upper || c >= 'A' && c <= 'Z'
	}
	if !digit || !upper {
		return -1
	}
	return end
}

// mailchimpKey is 32 hex digits, "-us" and the one or two digit data
// center number.
func mailchimpKey(v []byte, i int) (int, string) {
	end := lowerHexRun(v, i, 32)
	if end < 0 || !bytes.HasPrefix(v[end:], []byte("-us")) {
		return -1, ""
	}
	end = chars(1, 2, func(c byte) bool { return c >= '0' && c <= '9' })(v, end+3)
	if end < 0 || end < len(v) && isWord(v[end]) {
		return -1, ""
	}
	return end, EnvLive
}

// lowerHexRun matches exactly n lower-case hex digits.
func lowerHexRun(v []byte, i, n int) int {
	return chars(n, n, func(c byte) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' })(v, i)
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func isWord(c byte) bool { return isAlnum(c) || c == '_' }

func isUpperAlnum(c byte) bool { return c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' }

func isTokenByte(c byte) bool { return isAlnum(c) || c == '_' || c == '-' }

func isAdyenByte(c byte) bool {
	return isAlnum(c) || c == '+' || c == '/' || c == '=' || c == '.' || c == '_' || c == '-'
}

// rulesByFirst indexes tokenRules by the first byte their marks can
// start with, so the scan tries only the rules that could match.
var rulesByFirst = func() [256][]int {
	var idx [256][]int
	for i, r := range tokenRules {
		firsts := []byte(r.firsts)
		for _, m := range r.marks {
			firsts = append(firsts, m.text[0])
		}
		for _, c := range firsts {
			if !slices.Contains(idx[c], i) {
				idx[c] = append(idx[c], i)
			}
		}
	}
	return idx
}()

// keyToken is one key found by a token rule.
type keyToken struct {
	rule       *tokenRule
	start, end int
	env        string
}

// findTokens returns every key a token rule finds in v, in order and
// never overlapping. Each start is tried against the few rules indexed
// under its byte and a match is skipped whole, so the scan is linear.
func findTokens(v []byte, ctxOf func() string) []keyToken {
	var out []keyToken
	ctx, built := "", false
	runEnd := 0
	for i := 0; i < len(v); i++ {
		if i >= runEnd {
			// No key is shorter than minKeyLen bytes of key characters,
			// so a shorter run (a word, a number) is skipped whole.
			runEnd = i
			for runEnd < len(v) && isKeyByte(v[runEnd]) {
				runEnd++
			}
			if runEnd-i < minKeyLen {
				i = runEnd
				continue
			}
		}
		if i > 0 && isWord(v[i-1]) {
			continue
		}
		for _, ri := range rulesByFirst[v[i]] {
			r := &tokenRules[ri]
			if r.context != "" {
				if !built {
					ctx, built = ctxOf(), true
				}
				if !strings.Contains(ctx, r.context) {
					continue
				}
			}
			end, env := r.match(v, i)
			if end < 0 {
				continue
			}
			out = append(out, keyToken{rule: r, start: i, end: end, env: env})
			i = end - 1
			break
		}
	}
	return out
}

// minKeyLen is the length of the shortest key the table knows (AKIA and
// 16 characters).
const minKeyLen = 20

// isKeyByte reports whether c can be part of a key of the table.
func isKeyByte(c byte) bool {
	return isAlnum(c) || c == '_' || c == '-' || c == '$' || c == '.' || c == '+' || c == '/' || c == '='
}

// nameRule finds a key with no marker by where it is stored: the
// setting's name, the option or meta key of its row and the path to it.
type nameRule struct {
	provider string
	sev      core.Severity
	// match gets the lower-cased name of the setting and a context of the
	// lower-cased row key, path and file.
	match func(name, ctx string) bool
	// env decides the environment from the name and siblings; "" means
	// unknown.
	env func(name string, sib siblings) string
}

// mailProviders are the mail API providers a name can point at.
var mailProviders = []struct{ word, provider string }{
	{"sendgrid", "sendgrid"}, {"mailgun", "mailgun"}, {"postmark", "postmark"},
	{"sparkpost", "sparkpost"}, {"mandrill", "mandrill"}, {"sendinblue", "brevo"},
	{"brevo", "brevo"}, {"mailchimp", "mailchimp"}, {"resend", "resend"},
	{"mailersend", "mailersend"}, {"amazonses", "ses"}, {"ses_", "ses"},
}

// nameRules is the second half of the table: formats with no marker, by
// the setting that holds them (security.md S1, F17).
var nameRules = []nameRule{
	{provider: "paypal", sev: core.SeverityCritical,
		match: func(n, ctx string) bool {
			if strings.Contains(n, "paypal") {
				return hasWord(n, "secret") || strings.Contains(n, "client_id")
			}
			return (strings.Contains(ctx, "ppcp") || strings.Contains(ctx, "paypal")) &&
				(strings.HasPrefix(n, "client_secret") || strings.HasPrefix(n, "client_id") ||
					n == "api_password" || n == "api_signature")
		},
		env: func(n string, s siblings) string { return firstEnv(nameEnv(n), s.env()) }},
	{provider: "braintree", sev: core.SeverityCritical,
		match: func(n, ctx string) bool {
			return strings.Contains(ctx, "braintree") && (strings.HasSuffix(n, "private_key") || strings.HasSuffix(n, "public_key"))
		},
		env: fieldEnv},
	{provider: "square", sev: core.SeverityCritical,
		match: func(n, ctx string) bool {
			return strings.Contains(ctx, "square") && (strings.HasSuffix(n, "token") || strings.HasSuffix(n, "application_id"))
		},
		env: func(n string, s siblings) string { return firstEnv(nameEnv(n), s.env()) }},
	{provider: "klarna", sev: core.SeverityCritical,
		match: func(n, ctx string) bool { return strings.Contains(ctx, "klarna") && strings.Contains(n, "secret") },
		env:   fieldEnv},
	{provider: "authorize-net", sev: core.SeverityCritical,
		match: func(n, ctx string) bool {
			return strings.Contains(ctx, "authorize") && (strings.Contains(n, "login_id") || strings.Contains(n, "transaction_key") || strings.Contains(n, "signature_key"))
		},
		env: fieldEnv},
	{provider: "razorpay", sev: core.SeverityCritical,
		match: func(n, ctx string) bool { return strings.Contains(ctx, "razorpay") && strings.Contains(n, "secret") },
		env: func(n string, s siblings) string {
			id := s.get("key_id")
			switch {
			case strings.HasPrefix(id, "rzp_live_"):
				return EnvLive
			case strings.HasPrefix(id, "rzp_test_"):
				return EnvTest
			}
			return nameEnv(n)
		}},
	{provider: "twilio", sev: core.SeverityHigh,
		match: func(n, ctx string) bool {
			return (strings.Contains(n, "twilio") || strings.Contains(ctx, "twilio")) &&
				(strings.Contains(n, "token") || strings.Contains(n, "secret") || strings.Contains(n, "sid") || n == "auth")
		},
		env: func(n string, _ siblings) string {
			if hasWord(n, "test") {
				return EnvTest
			}
			return EnvLive
		}},
	{provider: "jetpack", sev: core.SeverityHigh,
		match: func(n, ctx string) bool {
			return strings.Contains(ctx, "jetpack") && (n == "blog_token" || strings.Contains(ctx, "user_tokens"))
		},
		env: func(string, siblings) string { return EnvLive }},
	{provider: "aws", sev: core.SeverityHigh,
		match: func(n, ctx string) bool {
			if strings.Contains(n, "secret_access_key") || strings.Contains(n, "secret-access-key") || strings.Contains(n, "aws_secret") {
				return true
			}
			return (strings.Contains(ctx, "as3cf") || strings.Contains(ctx, "amazon-s3")) && strings.Contains(n, "secret")
		},
		env: func(string, siblings) string { return EnvLive }},
	{provider: "smtp", sev: core.SeverityHigh,
		match: func(n, ctx string) bool {
			switch {
			case strings.Contains(ctx, "wp_mail_smtp"):
				return n == "pass" && strings.Contains(ctx, "smtp.pass")
			case strings.Contains(ctx, "postman_options"):
				return n == "basic_auth_password"
			case strings.Contains(ctx, "fluentmail"):
				return n == "password"
			}
			return n == "mail_password" || n == "smtp_password" || n == "smtp_pass" || n == "mail_smtp_password"
		},
		env: func(_ string, s siblings) string {
			h := s.first("host", "smtp_host", "mail_host", "hostname", "smtp_server", "server")
			switch {
			case h == "":
				return ""
			case loopbackHost(h):
				return EnvTest
			}
			return EnvLive
		}},
	{provider: "mail", sev: core.SeverityHigh,
		match: func(n, ctx string) bool { return mailProvider(n, ctx) != "" },
		env:   func(string, siblings) string { return EnvLive }},
}

// mailProvider returns the mail API provider a credential name points
// at: the provider named in the setting's name or path, with a key,
// token or secret word in the name.
func mailProvider(name, ctx string) string {
	if !strings.Contains(name, "key") && !strings.Contains(name, "token") && !strings.Contains(name, "secret") {
		return ""
	}
	for _, p := range mailProviders {
		if strings.Contains(name, p.word) || strings.Contains(ctx, p.word+".") || strings.Contains(ctx, p.word+"_") {
			return p.provider
		}
	}
	return ""
}

// siblings are the other settings of the option a value sits in, looked
// up without regard to case.
type siblings map[string]string

func (s siblings) get(name string) string {
	if v, ok := s[name]; ok {
		return strings.TrimSpace(v)
	}
	for k, v := range s {
		if strings.EqualFold(k, name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (s siblings) first(names ...string) string {
	for _, n := range names {
		if v := s.get(n); v != "" {
			return v
		}
	}
	return ""
}

// env reads the environment from the mode settings next to a key:
// testmode, test_mode, sandbox, sandbox_on, enable_sandbox and
// sandbox_mode say test when on; environment, mode and env name it;
// is_live and live_mode say live when on.
func (s siblings) env() string {
	for _, n := range []string{"testmode", "test_mode", "sandbox", "sandbox_on", "enable_sandbox", "sandbox_mode", "use_sandbox"} {
		switch v := strings.ToLower(s.get(n)); {
		case truthy(v):
			return EnvTest
		case falsy(v):
			return EnvLive
		}
	}
	for _, n := range []string{"environment", "mode", "env", "paypal_environment", "stripe_mode", "paypal_mode"} {
		switch strings.ToLower(s.get(n)) {
		case "production", "live", "prod":
			return EnvLive
		case "sandbox", "test", "testing", "development", "dev", "staging":
			return EnvTest
		}
	}
	for _, n := range []string{"is_live", "live_mode", "livemode"} {
		switch v := strings.ToLower(s.get(n)); {
		case truthy(v):
			return EnvLive
		case falsy(v) && v != "":
			return EnvTest
		}
	}
	return ""
}

// disabled reports whether the siblings switch the gateway off.
func (s siblings) disabled() bool {
	v := strings.ToLower(s.get("enabled"))
	return v != "" && falsy(v)
}

func truthy(v string) bool { return v == "yes" || v == "1" || v == "true" || v == "on" }

func falsy(v string) bool { return v == "no" || v == "0" || v == "false" || v == "off" }

// nameEnv reads the environment from a field name such as
// client_secret_production or TWILIO_TEST_TOKEN; "" when it names none.
func nameEnv(name string) string {
	for _, p := range nameParts(name) {
		switch p {
		case "production", "live", "prod":
			return EnvLive
		case "sandbox", "test":
			return EnvTest
		}
	}
	return ""
}

// fieldEnv is for plugins that keep live and test credentials in
// separate fields (test_shared_secret beside shared_secret): an
// unmarked field holds the live credential whatever mode is on.
func fieldEnv(name string, _ siblings) string {
	if e := nameEnv(name); e != "" {
		return e
	}
	return EnvLive
}

func firstEnv(envs ...string) string {
	for _, e := range envs {
		if e != "" {
			return e
		}
	}
	return ""
}

// nameParts splits a lower-cased name at _ - . : [ ] and spaces.
func nameParts(name string) []string {
	return strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == ':' || r == '[' || r == ']' || r == ' ' || r == '$'
	})
}

func hasWord(name, w string) bool { return slices.Contains(nameParts(name), w) }

// loopbackHost reports whether an SMTP host is this machine or a local
// mail catcher, where a password reaches nobody.
func loopbackHost(h string) bool {
	h = strings.ToLower(strings.TrimSpace(h))
	if host, _, ok := strings.Cut(h, ":"); ok && !strings.Contains(h, "]") && strings.Count(h, ":") == 1 {
		h = host
	}
	h = strings.Trim(h, "[]")
	switch h {
	case "localhost", "mailpit", "mailhog", "echo", "rhythm-echo":
		return true
	}
	if a, err := netip.ParseAddr(h); err == nil {
		return a.IsLoopback()
	}
	return strings.HasSuffix(h, ".localhost")
}

// placeholder reports whether a value only stands in for a key: empty,
// xxx, changeme, your-key-here and the like, a ${VAR}, or a value
// already masked with bullets.
func placeholder(v string) bool {
	if len(v) > 64 {
		// A stand-in is short; a long value is data.
		return false
	}
	l := strings.ToLower(strings.TrimSpace(v))
	switch {
	case l == "", strings.Contains(l, "•"), strings.Contains(l, "***"),
		strings.Trim(l, "x*.-_ ") == "",
		strings.HasPrefix(l, "${"), strings.HasPrefix(l, "{{"), strings.HasPrefix(l, "%") && strings.HasSuffix(l, "%"),
		strings.HasPrefix(l, "<") && strings.HasSuffix(l, ">"):
		return true
	}
	for _, w := range []string{"changeme", "change-me", "change_me", "placeholder", "your_", "your-", "yourkey", "example", "redacted", "xxxx"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	switch l {
	case "null", "nil", "none", "true", "false", "empty", "test", "secret", "password", "undefined", "0", "1":
		return true
	}
	return false
}

// ownSecrets are names a staging site needs for itself (its database,
// its cache, its salts), never a production credential to blank: the
// config checks compare salts with production and DB_HOST with
// production's; key.generic leaves them alone.
var ownSecrets = map[string]bool{
	"db_password": true, "db_user": true, "db_username": true, "db_name": true, "db_host": true,
	"db_database": true, "database_password": true, "redis_password": true, "wp_redis_password": true,
	"memcached_password": true, "wp_cache_key_salt": true, "app_key": true,
	"auth_key": true, "secure_auth_key": true, "logged_in_key": true, "nonce_key": true,
	"auth_salt": true, "secure_auth_salt": true, "logged_in_salt": true, "nonce_salt": true,
}

// nameOnlyKinds are kit rules that read a name = value shape out of
// running text. On a whole data value (a post, a comment) they flag
// prose like "password: see the email", so they only run on the
// name=value pair Roadie builds from the setting's own name.
var nameOnlyKinds = map[string]bool{
	"env": true, "yaml": true, "json-field": true, "assignment": true, "form-field": true,
	"php-serialized": true, "wp-config": true, "authorization": true, "cookie": true,
	"header-token": true, "wp-reset-key": true, "oauth-code": true, "key-path-token": true,
	"reset-link-token": true, "curl-user": true, "npmrc-auth": true,
}

// roadieKinds are kit rules for formats Roadie's own table reads with
// their live and test markers. When Roadie's rule declined a value (too
// short, a word after re_, no Adyen context), kit's looser pattern is not
// asked again.
var roadieKinds = map[string]bool{
	"stripe-key": true, "stripe-webhook-secret": true, "square-token": true, "braintree-token": true,
	"flutterwave-key": true, "sendgrid-key": true, "mailgun-key": true, "mailchimp-key": true,
	"brevo-key": true, "resend-key": true, "aws-access-key": true,
}

// scanInput is one value with what is known about where it sits.
type scanInput struct {
	v    []byte
	name string // lower-cased setting name: the last path key, the pair's name or the row's option or meta key
	ctx  string // lower-cased row key, path and file, for context rules
	// ctxOf, when set, builds ctx on first use: most values never need it.
	ctxOf func() string
	at    core.Location
	sib   siblings
}

// scanValue is ScanValue.
func scanValue(v []byte, at core.Location, siblings map[string]string) []Hit {
	in := scanInput{v: v, at: at, sib: siblings}
	_, key := LeafKey(at.Path)
	rowName := rowKey(at.Row)
	in.name = strings.ToLower(key)
	if in.name == "" || isIndex(in.name) {
		in.name = strings.ToLower(rowName)
	}
	in.ctxOf = func() string { return strings.ToLower(rowName + " " + at.Path + " " + at.File + " " + at.Table) }
	return scan(in)
}

// isIndex reports whether a path key is an array index, which names nothing.
func isIndex(k string) bool {
	for _, c := range k {
		if c < '0' || c > '9' {
			return false
		}
	}
	return k != ""
}

// rowKey returns the option_name, meta_key or name of a Location.Row.
func rowKey(row string) string {
	for p := range strings.SplitSeq(row, ",") {
		k, v, ok := strings.Cut(p, "=")
		if ok && (k == "option_name" || k == "meta_key" || k == "name" || k == "setting" || k == "key") {
			return v
		}
	}
	return ""
}

// scanPairs is ScanPairs.
func scanPairs(file string, pairs []core.Pair) []Hit {
	sib := make(siblings, len(pairs))
	for _, p := range pairs {
		sib[strings.ToLower(p.Name)] = p.Value
	}
	var out []Hit
	for _, p := range pairs {
		out = append(out, scan(scanInput{
			v:    []byte(p.Value),
			name: strings.ToLower(p.Name),
			ctx:  strings.ToLower(file + " " + p.Name),
			at:   core.Location{File: file, Line: p.Line},
			sib:  sib,
		})...)
	}
	return out
}

// context returns ctx, building it once.
func (in *scanInput) context() string {
	if in.ctxOf != nil {
		in.ctx, in.ctxOf = in.ctxOf(), nil
	}
	return in.ctx
}

// scan runs the three rule sets in order on one value.
func scan(in scanInput) []Hit {
	if len(in.v) <= 64 && placeholder(string(in.v)) {
		return nil
	}
	var out []Hit
	for _, t := range findTokens(in.v, func() string { return in.context() + " " + in.name }) {
		val := string(in.v[t.start:t.end])
		env := t.env
		if env == "" {
			env = classifyBySiblings(t.rule.provider, in.name, in.sib)
		}
		out = append(out, newHit(t.rule.provider, t.rule.sev, env, val, in))
	}
	if len(out) > 0 {
		return out
	}
	val := strings.TrimSpace(string(in.v))
	if in.name != "" && credentialShaped(val) {
		for _, r := range nameRules {
			if !r.match(in.name, in.context()) {
				continue
			}
			provider := r.provider
			if provider == "mail" {
				provider = mailProvider(in.name, in.context())
			}
			return []Hit{newHit(provider, r.sev, r.env(in.name, in.sib), val, in)}
		}
	}
	if ownSecrets[in.name] {
		return nil
	}
	return genericHits(in)
}

// credentialShaped reports whether a value marked as a credential by its
// name could be one: no spaces and 8 or more bytes, or a JSON or
// serialized value never.
func credentialShaped(v string) bool {
	if len(v) < 8 || len(v) > 4096 || placeholder(v) {
		return false
	}
	return !strings.ContainsAny(v, " \t\r\n{};")
}

// genericHits runs kit's rules: token formats on the value itself and
// every rule on name=value when the setting has a name.
func genericHits(in scanInput) []Hit {
	var out []Hit
	seen := map[string]bool{}
	add := func(s secrets.Secret, nameForm bool) {
		if !nameForm && nameOnlyKinds[s.Kind] || roadieKinds[s.Kind] || seen[s.Masked] {
			return
		}
		if ownSecrets[strings.ToLower(s.Name)] {
			return
		}
		seen[s.Masked] = true
		h := newHit(ProviderGeneric, core.SeverityHigh, EnvLive, "", in)
		h.Masked = s.Masked
		out = append(out, h)
	}
	if mayHoldToken(in.v) {
		for _, s := range secrets.FindSecrets(in.v) {
			add(s, false)
		}
	}
	if len(out) == 0 && secretishName(in.name) && !bytes.ContainsAny(in.v, "\r\n\"") {
		line := in.name + "=\"" + string(in.v) + "\""
		for _, s := range secrets.FindSecrets([]byte(line)) {
			add(s, true)
		}
	}
	return out
}

// mayHoldToken is a cheap test of whether kit's token rules could match
// v, which keeps its regular expressions off the plain text that makes up
// most of a dump. Every kit token format has a run of 20 or more
// characters from [A-Za-z0-9_.+=~$-] holding a letter and a digit; the
// formats without one are a PEM private key and a URL with a password.
func mayHoldToken(v []byte) bool {
	run, letter, digit := 0, false, false
	for _, c := range v {
		switch {
		case c >= '0' && c <= '9':
			digit = true
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			letter = true
		case c == '_' || c == '.' || c == '+' || c == '=' || c == '~' || c == '$' || c == '-':
		default:
			run, letter, digit = 0, false, false
			continue
		}
		run++
		if run >= 20 && letter && digit {
			return true
		}
	}
	return bytes.Contains(v, []byte("PRIVATE KEY")) || bytes.Contains(v, []byte("://")) && bytes.IndexByte(v, '@') >= 0
}

// secretishName reports whether a setting name holds one of the words
// kit's name rules look for, so the name=value form is worth building.
func secretishName(name string) bool {
	if name == "" || notSecretNames[name] {
		return false
	}
	for _, w := range []string{"pass", "pw", "secret", "token", "key", "auth", "dsn", "credential", "otp"} {
		if strings.Contains(name, w) {
			return true
		}
	}
	return false
}

// notSecretNames are settings whose names look secret to kit and whose
// values are not credentials: WooCommerce's order key is in every order
// email and URL; WordPress lock and session keys are bookkeeping.
var notSecretNames = map[string]bool{
	"_order_key": true, "order_key": true, "_edit_lock": true, "session_key": true,
	"_wp_session_key": true, "meta_key": true, "option_key": true, "_transient_key": true,
}

// newHit builds one hit, masked with core.MaskKey.
func newHit(provider string, sev core.Severity, env, val string, in scanInput) Hit {
	if env == "" {
		env = EnvUnknown
	}
	h := Hit{ID: core.KeyLiveID(provider), Provider: provider, Env: env, At: in.at, Masked: core.MaskKey(val)}
	if provider == ProviderGeneric {
		h.ID = core.FindingKeyGeneric
	}
	switch env {
	case EnvLive:
		h.Severity = sev
		if in.sib.disabled() {
			h.Severity = core.SeverityLow
		}
	case EnvUnknown:
		h.Severity = core.SeverityMedium
	default:
		h.Severity = core.SeverityNone
	}
	return h
}

// classifyBySiblings decides a marker-less key's environment from its
// field name and the settings next to it. A Stripe webhook secret also
// follows a Stripe secret key beside it.
func classifyBySiblings(provider, name string, sib siblings) string {
	if e := nameEnv(name); e != "" {
		return e
	}
	if e := sib.env(); e != "" {
		return e
	}
	if provider == "stripe-webhook" {
		for _, v := range sib {
			switch {
			case strings.HasPrefix(v, "sk_live_"):
				return EnvLive
			case strings.HasPrefix(v, "sk_test_"):
				return EnvTest
			}
		}
	}
	return ""
}

// classify is Classify.
func classify(provider, value string, siblings map[string]string) string {
	value = strings.TrimSpace(value)
	p := baseProvider(provider)
	for _, t := range findTokens([]byte(value), func() string { return p }) {
		if t.rule.provider != p {
			continue
		}
		if t.env != "" {
			return t.env
		}
		break
	}
	sib := siblingsOf(siblings)
	if e := classifyBySiblings(p, sib.get("name"), sib); e != "" {
		return e
	}
	return EnvUnknown
}

func siblingsOf(m map[string]string) siblings { return m }

// baseProvider maps a keys.test_keys name or a hyphenated one to the
// provider of the rule table: stripe_publishable is stripe-publishable,
// paypal-client-secret is paypal.
func baseProvider(p string) string {
	p = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(p)), "_", "-")
	switch {
	case strings.HasPrefix(p, "paypal"):
		return "paypal"
	case strings.HasPrefix(p, "razorpay"):
		return "razorpay"
	}
	return p
}
