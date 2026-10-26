// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

// Action is what a rule does to a value, a row or a table.
type Action string

// The rule actions.
const (
	Fake          Action = "fake"
	Blank         Action = "blank"
	SetNull       Action = "null"
	Keep          Action = "keep"
	Truncate      Action = "truncate"
	DropTable     Action = "drop"
	DeleteRow     Action = "delete-row"
	Scrub         Action = "scrub"
	RewriteURL    Action = "rewrite-url"
	SwapKey       Action = "swap-key"
	Fixed         Action = "fixed"
	ResetPassword Action = "reset-password"
)

// FakeKind is the kind of personal value a fake stands in for.
type FakeKind string

// The fake kinds of PRD 11.1.
const (
	KEmail     FakeKind = "email"
	KUsername  FakeKind = "username"
	KNicename  FakeKind = "nicename"
	KFirstName FakeKind = "first_name"
	KLastName  FakeKind = "last_name"
	KFullName  FakeKind = "full_name"
	KPhone     FakeKind = "phone"
	KStreet    FakeKind = "street"
	KAddress   FakeKind = "address"
	KCity      FakeKind = "city"
	KPostcode  FakeKind = "postcode"
	KCompany   FakeKind = "company"
	KIP        FakeKind = "ip"
	KUserAgent FakeKind = "user_agent"
	KDOB       FakeKind = "dob"
	KURL       FakeKind = "url"
	KText      FakeKind = "text"
)

// FakeKinds lists every fake kind in a fixed order.
func FakeKinds() []FakeKind {
	return []FakeKind{KEmail, KUsername, KNicename, KFirstName, KLastName, KFullName, KPhone,
		KStreet, KAddress, KCity, KPostcode, KCompany, KIP, KUserAgent, KDOB, KURL, KText}
}

// Rule is one compiled profile or config rule.
type Rule struct {
	ID      string // "<profile>.<table>.<column>[.<path>]", e.g. "woocommerce.wc_orders.billing_email"
	Profile string
	Table   string // glob; "{prefix}" and "{site}" expanded by the profile compiler
	Column  string
	Where   map[string]string // column -> glob on the same row, e.g. {"meta_key": "_billing_email"}
	Path    string            // inside serialized/JSON, e.g. "secret_key", "billing.email"; "" = whole value
	Action  Action
	Fake    FakeKind
	Value   string // for Fixed
	Explain string
}

// Change counts what one rule did in a run.
type Change struct {
	Rule   string
	Table  string
	Column string
	Action Action
	Rows   int64 // rows the rule matched
	Values int64 // values it changed
	Kept   int64 // values it left alone (kept users, already fake, empty)
}
