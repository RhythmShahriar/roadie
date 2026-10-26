// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package detect

import (
	"cmp"
	"slices"

	"github.com/RhythmShahriar/roadie/core"
)

// textSpanner finds the personal values the scrubber replaces in free
// text: personal emails, strong-format phones, grouped cards and public
// IPs. It applies every detector guard but the allow lists, which belong
// to verify: a business address on the allow list is still replaced in a
// customer's order note.
type textSpanner struct{}

// Spans returns the values in text, sorted and without overlap; where two
// overlap the one that starts first (then the longer) wins.
func (textSpanner) Spans(text []byte) []core.Span {
	var out []core.Span
	scanEmails(text, func(m emailMatch) {
		if classifyEmail(m) == emailPersonal {
			out = append(out, core.Span{Start: m.start, End: m.end, Kind: core.KEmail})
		}
	})
	scanPhones(text, false, func(m phoneMatch) {
		out = append(out, core.Span{Start: m.start, End: m.end, Kind: core.KPhone})
	})
	scanCards(text, false, func(m cardMatch) {
		out = append(out, core.Span{Start: m.start, End: m.end, Kind: KCard})
	})
	ipSpans(text, func(start, end int) {
		out = append(out, core.Span{Start: start, End: end, Kind: core.KIP})
	})
	if len(out) < 2 {
		return out
	}
	slices.SortFunc(out, func(a, b core.Span) int {
		return cmp.Or(cmp.Compare(a.Start, b.Start), cmp.Compare(b.End, a.End))
	})
	kept := out[:1]
	for _, s := range out[1:] {
		if s.Start >= kept[len(kept)-1].End {
			kept = append(kept, s)
		}
	}
	return kept
}
