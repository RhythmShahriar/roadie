// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import (
	"net/netip"
	"slices"
	"strings"
)

// The reserved ranges are the one source of truth for fakes and
// detectors: every fake Roadie makes falls in one of them; no detector
// flags a value in one.

// ReservedEmailDomain reports whether domain can never belong to a real
// mailbox: example.com, example.net and example.org (RFC 2606) and the
// reserved top-level names example, invalid, test, localhost (RFC 2606)
// and local (RFC 6762), with any subdomain.
func ReservedEmailDomain(domain string) bool {
	d := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	if d == "" {
		return false
	}
	for _, r := range []string{"example.com", "example.net", "example.org"} {
		if d == r || strings.HasSuffix(d, "."+r) {
			return true
		}
	}
	tld := d[strings.LastIndexByte(d, '.')+1:]
	return slices.Contains([]string{"example", "invalid", "test", "localhost", "local"}, tld)
}

// digitsOf keeps the digits of s.
func digitsOf(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= '0' && c <= '9' {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// ukDrama are Ofcom's geographic and freephone numbers for TV and radio
// drama (ofcom.org.uk/phones-and-broadband/phone-numbers/numbers-for-drama),
// in E.164 without the +. Each prefix covers the 1,000 numbers that follow
// it by three digits; Ofcom never allocates them to a subscriber, so sample
// data and theme demos use them. The mobile range 07700 900xxx is FictionalPhone's own case.
var ukDrama = []string{
	"441134960", // Leeds 0113 496 0000 to 0999
	"441144960", // Sheffield
	"441154960", // Nottingham
	"441164960", // Leicester
	"441174960", // Bristol
	"441184960", // Reading
	"441214960", // Birmingham
	"441314960", // Edinburgh
	"441414960", // Glasgow
	"441514960", // Liverpool
	"441614960", // Manchester
	"441914980", // Tyneside 0191 498 0000 to 0999
	"442079460", // London 020 7946 0000 to 0999
	"442920180", // Cardiff 029 2018 0000 to 0999
	"442896496", // Northern Ireland 028 9649 6000 to 6999
	"441632960", // no area: 01632 960000 to 960999
	"448081570", // freephone 08081 570000 to 570999
}

// germanDrama are the Bundesnetzagentur's numbers for media productions
// ("Drama Numbers", bundesnetzagentur.de/DE/Fachthemen/Telekommunikation/
// Nummerierung/DramaNumbers), in E.164 without the +. A prefix shorter
// than a full number covers every number that continues it by the
// listed count of digits.
var germanDrama = []struct {
	prefix string
	rest   int // digits after the prefix
}{
	{"493023125", 3},     // Berlin 030 23125 000 to 999
	{"496990009", 3},     // Frankfurt am Main 069 90009 000 to 999
	{"494066969", 3},     // Hamburg 040 66969 000 to 999
	{"492214710", 3},     // Köln 0221 4710 000 to 999
	{"498999998", 3},     // München 089 99998 000 to 999
	{"4915228817386", 0}, // mobile 0152 28817386
	{"4915228895456", 0},
	{"4915254599371", 0},
	{"4917139200", 2}, // 0171 39200 00 to 99
	{"491729925904", 0},
	{"491729968532", 0},
	{"491729973185", 0},
	{"491729973186", 0},
	{"491729980752", 0},
	{"491749091317", 0},
	{"491749464308", 0},
	{"49176040690", 2}, // 0176 040690 00 to 99
}

// FictionalPhone reports whether a number (E.164 digits, a leading + and
// separators allowed) is in a range kept for fiction: NANP 555-0100 to
// 555-0199 in any area code and the 555 01xx numbers Roadie's echo style
// makes (+1 555 01x xxxx), Ofcom's UK drama mobiles 07700 900000 to
// 900999 and its geographic and freephone drama ranges (ukDrama) and the
// German drama numbers.
func FictionalPhone(e164digits string) bool {
	d := digitsOf(e164digits)
	switch {
	case len(d) == 11 && d[0] == '1':
		return d[4:9] == "55501" || d[1:6] == "55501"
	case len(d) == 12 && strings.HasPrefix(d, "447700900"):
		return true
	case len(d) == 12:
		for _, p := range ukDrama {
			if strings.HasPrefix(d, p) {
				return true
			}
		}
	}
	for _, g := range germanDrama {
		if strings.HasPrefix(d, g.prefix) && len(d) == len(g.prefix)+g.rest {
			return true
		}
	}
	return false
}

// documentationPrefixes are the address blocks kept for documentation:
// RFC 5737 for IPv4, RFC 3849 and RFC 9637 for IPv6.
var documentationPrefixes = []netip.Prefix{
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("3fff::/20"),
}

// DocumentationIP reports whether ip is in a documentation range.
func DocumentationIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	for _, p := range documentationPrefixes {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// testCards are the published test card numbers of Stripe
// (docs.stripe.com/testing) and Braintree
// (developer.paypal.com/braintree/docs/guides/credit-cards/testing-go-live).
var testCards = []string{
	// Stripe
	"4242424242424242", "4000056655665556", "5555555555554444", "2223003122003222",
	"5200828282828210", "5105105105105100", "378282246310005", "371449635398431",
	"6011111111111117", "6011000990139424", "6011981111111113", "3056930009020004",
	"36227206271667", "6555900000604105", "3566002020360505", "6200000000000005",
	"6200000000000047", "6205500000000000004", "4000002500001001", "4000000000000077",
	"4000000000000002", "4000000000009995", "4000000000000069", "4000000000000127",
	"4000000000000119", "4000000000003220", "4000002760003184", "4000000000003063",
	"4000002500003155", "4000000000000341", "4000000000009235", "4000000000000101",
	// Braintree
	"4111111111111111", "4005519200000004", "4009348888881881", "4012000033330026",
	"4012000077777777", "4012888888881881", "4217651111111119", "4500600000000061",
	"2223000048400011", "3530111333300000", "6304000000000000", "4000111111111115",
	"36259600000004", "6243030000000001", "5500000000000004", "4222222222222",
}

// TestCard reports whether digits (separators allowed) is a published test card.
func TestCard(digits string) bool { return slices.Contains(testCards, digitsOf(digits)) }

// exampleIBANs are the IBANs banks and the ECBS publish as examples.
var exampleIBANs = []string{
	"GB82WEST12345698765432", "GB33BUKB20201555555555", "DE89370400440532013000",
	"FR1420041010050500013M02606", "NL91ABNA0417164300", "ES9121000418450200051332",
	"IT60X0542811101000000123456", "BE68539007547034", "CH9300762011623852957",
	"AT611904300234573201",
}

// ExampleIBAN reports whether s (spaces allowed, any case) is a documented example IBAN.
func ExampleIBAN(s string) bool {
	return slices.Contains(exampleIBANs, strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), " ", "")))
}
