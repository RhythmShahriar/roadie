// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package seed fills the testbed's scenario templates (testbed/fixtures/<id>/)
// with canaries and fake keys and writes them under testbed/runtime/: the
// input dumps and file trees, runtime/canaries.json (every canary with its
// ID, kind, scenario, table, column and value) and, per scenario, the list
// of cells that hold valid serialized PHP for the oracle's integrity check.
//
// Canaries look real, as they must for verify to be tested: names, emails
// at real mail domains, phones in real (not fictional) ranges, public IPs
// and card numbers that pass Luhn and are not test cards. They are made at
// seed time from the seed and the scenario ID, never committed; they only
// ever reach a report as their ID. The sealed network is what keeps them
// from being mailed.
package seed

import (
	"fmt"
	"hash/fnv"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// Canary is one seeded value as canaries.json lists it. Kind "key" is an
// API key; Live tells a live key from a test key.
type Canary struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Scenario  string   `json:"scenario"`
	Table     string   `json:"table,omitempty"`
	Column    string   `json:"column,omitempty"`
	Value     string   `json:"value"`
	Live      bool     `json:"live,omitempty"`
	Encodings []string `json:"encodings,omitempty"`
}

// Kinds lists the canary kinds a template can ask for.
// stripe_customer is a Stripe customer ID (cus_...), which links a fake
// back to a real person at Stripe; token is a one-time secret such as a
// password reset key in a mail log.
var Kinds = []string{"email", "first_name", "last_name", "full_name", "phone", "street", "ip", "card", "user_agent", "stripe_customer", "token"}

// generator makes canary values for one scenario. Values come from the
// seed and the scenario ID, so adding a scenario leaves the others' values
// as they were.
type generator struct {
	r *rand.Rand
}

func newGenerator(seed int64, scenario string) *generator {
	h := fnv.New64a()
	_, _ = h.Write([]byte(scenario))
	// #nosec G404 G115 -- fake test data from a seed, never a secret
	return &generator{r: rand.New(rand.NewPCG(uint64(seed), h.Sum64()))}
}

func (g *generator) word(n int) string {
	const cons, vows = "bcdfghjklmnprstvz", "aeiou"
	var b strings.Builder
	for i := range n {
		if i%2 == 0 {
			b.WriteByte(cons[g.r.IntN(len(cons))])
		} else {
			b.WriteByte(vows[g.r.IntN(len(vows))])
		}
	}
	return b.String()
}

func (g *generator) digits(n int) string {
	var b strings.Builder
	for range n {
		b.WriteByte("0123456789"[g.r.IntN(10)])
	}
	return b.String()
}

func title(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

// mailDomains are real mail providers: an address there is what verify
// must flag. The sealed network means nothing is ever delivered.
var mailDomains = []string{"gmail.com", "outlook.com", "yahoo.com", "hotmail.co.uk", "icloud.com", "gmx.de", "web.de", "proton.me"}

// publicFirstOctets start public unicast IPv4 ranges (no private,
// documentation, CGNAT or multicast block starts with these).
var publicFirstOctets = []int{31, 46, 62, 78, 81, 86, 92, 109, 145, 178, 185, 212}

// value returns a fresh value of kind. Every value is at least 8 bytes,
// the oracle's matcher window.
func (g *generator) value(kind string) (string, error) {
	switch kind {
	case "email":
		const letters = "abcdefghijklmnopqrstuvwxyz"
		tag := letters[g.r.IntN(26):][:1] + g.digits(2) + letters[g.r.IntN(26):][:1] + g.digits(2)
		return g.word(4+g.r.IntN(3)) + "." + g.word(6+g.r.IntN(3)) + "." + tag + "@" + mailDomains[g.r.IntN(len(mailDomains))], nil
	case "first_name":
		return title(g.word(8)), nil
	case "last_name":
		return title(g.word(10)), nil
	case "full_name":
		return title(g.word(7)) + " " + title(g.word(9)), nil
	case "phone":
		switch g.r.IntN(3) {
		case 0:
			// 07911 is a real UK mobile range, outside Ofcom's 07700 900 drama block.
			return "+44 7911 " + g.digits(6), nil
		case 1:
			// A Chicago number; real exchanges, never the fictional 555.
			return "+1 312 " + strconv.Itoa(200+g.r.IntN(354)) + " " + g.digits(4), nil
		}
		return "+49 151 " + g.digits(8), nil
	case "street":
		suffix := []string{"Road", "Street", "Lane", "Avenue", "Close"}[g.r.IntN(5)]
		return fmt.Sprintf("%d %s %s", 10+g.r.IntN(890), title(g.word(9)), suffix), nil
	case "ip":
		return fmt.Sprintf("%d.%d.%d.%d", publicFirstOctets[g.r.IntN(len(publicFirstOctets))], 10+g.r.IntN(240), 10+g.r.IntN(240), 100+g.r.IntN(150)), nil
	case "card":
		for {
			n := "4" + g.digits(14)
			n += strconv.Itoa(luhnCheck(n))
			if !core.TestCard(n) {
				return n, nil
			}
		}
	case "stripe_customer":
		return "cus_" + g.word(6) + g.digits(8), nil
	case "token":
		return g.word(6) + g.digits(4) + g.word(6) + g.digits(4), nil
	case "user_agent":
		return fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.%s.%d Safari/537.36", g.digits(5), 10+g.r.IntN(90)), nil
	}
	return "", fmt.Errorf("unknown canary kind %q (kinds: %s)", kind, strings.Join(Kinds, ", "))
}

// luhnCheck returns the check digit that makes digits+check pass Luhn.
func luhnCheck(digits string) int {
	sum := 0
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if (len(digits)-i)%2 == 1 {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return (10 - sum%10) % 10
}

// luhnValid reports whether a digit string passes the Luhn check.
func luhnValid(n string) bool {
	return len(n) > 1 && luhnCheck(n[:len(n)-1]) == int(n[len(n)-1]-'0')
}
