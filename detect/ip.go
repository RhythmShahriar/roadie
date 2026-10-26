// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"bytes"
	"net/netip"
	"slices"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// IPs are flagged only where they are known to be IPs: IP columns and
// leaves under an "ip" key (verify passes those with ColumnKind ip). A
// dotted number in free text is as often a version as an address.

// ipColumn reports whether the value sits in an IP column: ip, ip_address,
// *_ip, comment_author_IP, _customer_ip_address, user_ip_address and the like.
func ipColumn(sc core.ScanContext) bool {
	if sc.ColumnKind == core.KIP {
		return true
	}
	return anyNameTest(sc, testIP, func(name string, tokens []string) bool {
		return hasToken(tokens, "ip", "ipaddress", "ipv4", "ipv6") || name == "remote_addr"
	})
}

// notPersonalPrefixes are the special-purpose blocks of RFC 6890 that
// netip has no method for: CGNAT, this network, IETF protocol
// assignments, benchmarking and the reserved 240/4.
var notPersonalPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
}

// publicIP reports whether a can belong to a person's connection: not
// loopback, private, link-local, multicast, unspecified, CGNAT, reserved
// or in a documentation range.
func publicIP(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() || a.IsInterfaceLocalMulticast() || a.IsMulticast() ||
		a.IsUnspecified() || core.DocumentationIP(a) {
		return false
	}
	for _, p := range notPersonalPrefixes {
		if p.Contains(a) {
			return false
		}
	}
	return true
}

// parseIPToken reads an address, with an optional port or IPv6 brackets.
func parseIPToken(tok []byte) (netip.Addr, bool) {
	if len(tok) < 3 || len(tok) > 64 {
		return netip.Addr{}, false
	}
	s := string(tok)
	if a, err := netip.ParseAddr(s); err == nil {
		return a, true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr(), true
	}
	return netip.Addr{}, false
}

// ipDetector finds public IPs in IP columns (pii.ip).
type ipDetector struct{}

// ID returns pii.ip.
func (ipDetector) ID() string { return core.FindingPIIIP }

// Scan emits a hit for each public address in an IP column. A column may
// list several (X-Forwarded-For), separated by commas or spaces.
func (ipDetector) Scan(v []byte, sc core.ScanContext, emit func(core.Hit)) {
	if !ipColumn(sc) {
		return
	}
	for i := 0; i < len(v); {
		for i < len(v) && isIPSep(v[i]) {
			i++
		}
		j := i
		for j < len(v) && !isIPSep(v[j]) {
			j++
		}
		if a, ok := parseIPToken(v[i:j]); ok && publicIP(a) {
			emit(core.Hit{ID: core.FindingPIIIP, Severity: core.SeverityMedium, Start: i, End: j,
				Masked: core.MaskIP(a.String()), Encoding: "raw"})
		}
		i = j
	}
}

func isIPSep(c byte) bool {
	return c == ',' || c == ';' || c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// ipSpans finds public addresses in free text for the scrubber: dotted
// quads and IPv6 with at least two colons, not touching letters, digits
// or dots and not after a "v".
func ipSpans(v []byte, fn func(start, end int)) {
	if bytes.IndexByte(v, '.') < 0 && bytes.IndexByte(v, ':') < 0 {
		return
	}
	for i := 0; i < len(v); {
		c := v[i]
		if !isHexOrColon(c) && c != '.' {
			i++
			continue
		}
		j := i
		for j < len(v) && (isHexOrColon(v[j]) || v[j] == '.') && j-i <= 45 {
			j++
		}
		if end, ok := ipTokenAt(v, i, j); ok {
			fn(i, end)
		}
		// Move past the token, or one byte when it ran over the cap.
		i = max(j, i+1)
	}
}

// ipTokenAt reports whether v[i:j], less trailing dots and colons, is a
// public address standing alone; it also returns where it ends.
func ipTokenAt(v []byte, i, j int) (int, bool) {
	tok := bytes.TrimRight(v[i:j], ".:")
	j = i + len(tok)
	before, after := runeBefore(v, i), runeAt(v, j)
	if isWordRune(before) || before == '.' || before == ':' || isWordRune(after) {
		return 0, false
	}
	if after == '.' && isDigitRune(runeAt(v, j+1)) {
		return 0, false
	}
	colons := bytes.Count(tok, []byte(":"))
	if colons == 1 || colons == 0 && bytes.Count(tok, []byte(".")) != 3 {
		return 0, false
	}
	if colons == 0 && afterVersionWord(v, i) {
		return 0, false
	}
	a, ok := parseIPToken(tok)
	return j, ok && publicIP(a) && (a.Is4() || colons >= 2)
}

// versionWords are the words that make a dotted quad after them a version.
var versionWords = []string{"version", "ver", "v", "release", "build"}

// afterVersionWord reports whether the word before v[i] (one space or
// "v" glued on) says a version follows: "version 1.2.3.4".
func afterVersionWord(v []byte, i int) bool {
	j := i
	for j > 0 && (v[j-1] == ' ' || v[j-1] == ':') && i-j < 2 {
		j--
	}
	k := j
	for k > 0 && (v[k-1] >= 'a' && v[k-1] <= 'z' || v[k-1] >= 'A' && v[k-1] <= 'Z') && j-k < 8 {
		k--
	}
	w := strings.ToLower(string(v[k:j]))
	return slices.Contains(versionWords, w)
}

func isHexOrColon(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' || c == ':'
}
