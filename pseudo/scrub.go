// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"bytes"
	"sort"
	"unicode"
	"unicode/utf8"

	"github.com/RhythmShahriar/roadie/core"
)

// redacted replaces spans no fake kind covers, such as card numbers.
var redacted = []byte("XXXX")

// scrubber is the state of one Scrub call.
type scrubber struct {
	text  []byte
	known core.Known
	f     core.Faker
	out   []byte // nil until the first replacement
	pair  []byte // reused for each word pair looked up
	last  int    // end of the text already copied to out
	n     int
}

func scrub(text []byte, sp core.Spanner, known core.Known, f core.Faker) ([]byte, int) {
	s := &scrubber{text: text, known: known, f: f}
	// Scrub looks for addresses itself as well: a Spanner that reads
	// only ASCII would stop at the first accented letter and leave the
	// rest of the address behind.
	found := emailSpans(text)
	if sp != nil {
		found = append(found, sp.Spans(text)...)
	}
	spans := cleanSpans(text, found)
	pos := 0
	for _, sp := range spans {
		s.names(pos, sp.Start)
		pos = sp.End
		v := text[sp.Start:sp.End]
		if IsFake(sp.Kind, v) {
			continue // already a Roadie fake: keeps Scrub idempotent
		}
		s.replace(sp.Start, sp.End, s.fake(sp.Kind, v))
	}
	s.names(pos, len(text))
	if s.out == nil {
		return text, 0
	}
	return append(s.out, text[s.last:]...), s.n
}

// replace writes repl for text[start:end].
func (s *scrubber) replace(start, end int, repl []byte) {
	if s.out == nil {
		s.out = make([]byte, 0, len(s.text)+32)
	}
	s.out = append(s.out, s.text[s.last:start]...)
	s.out = append(s.out, repl...)
	s.last = end
	s.n++
}

// fake returns the faker's value for a span, or XXXX when there is no
// faker, no fake kind or the faker refuses.
func (s *scrubber) fake(kind core.FakeKind, v []byte) []byte {
	if s.f == nil || kind == core.KText || !knownKind(kind) {
		return redacted
	}
	out, err := s.f.Fake(kind, v, core.FakeOpts{})
	if err != nil || len(out) == 0 {
		return redacted
	}
	return out
}

// names replaces the known names in text[start:end]: a known word pair
// first, then a capitalised known word of 3 or more letters.
//
// A single word must start with a capital letter: a customer named Will,
// Mark or Green would otherwise turn "we will mark the green box" into
// nonsense. A pair is matched in any case, since two known words in a
// row are far more likely a name than prose.
//
// A pair joined by white space is a full name and gets a two-word fake.
// A pair joined by a hyphen or an apostrophe (O'Sullivan, Jean-Luc) is
// one name word and gets a one-word fake of the kind Known reports. The
// fake is keyed on the text as written, so a name in a note gets the
// same fake as the same name in its column.
//
// A pair joined by white space that Known lacks but that reads as a
// Roadie fake full name (Kylie Kim) is left whole, words and all: it is
// most likely a fake from an earlier pass and rewriting a word of it
// because another customer is named Kim would make Scrub change its own
// output. A real customer named Kylie Kim is still found, as Known then
// holds the pair. A single list word on its own (Kylie) can't be told
// apart from a real name Known holds and is rewritten again.
func (s *scrubber) names(start, end int) {
	if s.known == nil || start >= end {
		return
	}
	words := nameWords(s.text[start:end])
	for i := 0; i < len(words); i++ {
		a := [2]int{start + words[i][0], start + words[i][1]}
		if i+1 < len(words) {
			b := [2]int{start + words[i+1][0], start + words[i+1][1]}
			if gap := s.text[a[1]:b[0]]; pairGap(gap) {
				s.pair = append(append(append(s.pair[:0], s.text[a[0]:a[1]]...), ' '), s.text[b[0]:b[1]]...)
				kind, _, ok := s.known.Contains(s.pair)
				if ok && isNameKind(kind) {
					kind = core.KFullName
					if !bytes.ContainsFunc(gap, unicode.IsSpace) {
						kind = oneWordKind(s.known, s.text[a[0]:b[1]])
					}
					s.replace(a[0], b[1], s.fake(kind, s.text[a[0]:b[1]]))
					i++
					continue
				}
				if bytes.ContainsFunc(gap, unicode.IsSpace) && IsFake(core.KFullName, s.pair) {
					i++ // a fake full name: keeps Scrub idempotent
					continue
				}
			}
		}
		word := s.text[a[0]:a[1]]
		if utf8.RuneCount(word) < minWordLetters || !capitalised(word) {
			continue
		}
		if kind, _, ok := s.known.Contains(word); ok && isNameKind(kind) {
			if kind == core.KFullName {
				kind = core.KFirstName // one word stands in for one word
			}
			s.replace(a[0], a[1], s.fake(kind, word))
		}
	}
}

// capitalised reports whether word starts with an upper or title case
// letter.
func capitalised(word []byte) bool {
	r, _ := utf8.DecodeRune(word)
	return unicode.IsUpper(r) || unicode.IsTitle(r)
}

// oneWordKind is the kind of fake for a joined name such as O'Sullivan:
// the kind Known has for it as written, a last name when Known only has
// it as part of a full name.
func oneWordKind(known core.Known, name []byte) core.FakeKind {
	if kind, _, ok := known.Contains(name); ok && kind != core.KFullName && isNameKind(kind) {
		return kind
	}
	return core.KLastName
}

// pairGap reports whether the bytes between two words join them into one
// name: white space, hyphens and apostrophes only.
func pairGap(b []byte) bool {
	if len(b) == 0 || len(b) > 3 {
		return false
	}
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if !unicode.IsSpace(r) && r != '-' && r != '\'' && r != '’' {
			return false
		}
		i += size
	}
	return true
}

// cleanSpans sorts the spans, drops any outside text or empty, widens
// email spans over the Unicode letters around them and merges overlaps,
// so a Spanner that misreads an address can't leave part of it behind.
func cleanSpans(text []byte, in []core.Span) []core.Span {
	spans := make([]core.Span, 0, len(in))
	for _, sp := range in {
		if sp.Start < 0 || sp.End > len(text) || sp.Start >= sp.End {
			continue
		}
		if sp.Kind == core.KEmail {
			sp.Start, sp.End = widenEmail(text, sp.Start, sp.End)
		}
		spans = append(spans, sp)
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].Start < spans[j].Start })
	out := spans[:0]
	for _, sp := range spans {
		if n := len(out); n > 0 && sp.Start < out[n-1].End {
			if sp.End > out[n-1].End {
				out[n-1].End = sp.End
			}
			continue
		}
		out = append(out, sp)
	}
	return out
}

// widenEmail moves an email span's start back over local-part runes and
// its end forward over domain runes, then off a closing dot or hyphen.
func widenEmail(text []byte, start, end int) (int, int) {
	for start > 0 {
		r, size := utf8.DecodeLastRune(text[:start])
		if !isLocalRune(r) {
			break
		}
		start -= size
	}
	for end < len(text) {
		r, size := utf8.DecodeRune(text[end:])
		if !isDomainRune(r) {
			break
		}
		end += size
	}
	for end > start && (text[end-1] == '.' || text[end-1] == '-') {
		end--
	}
	return start, end
}

func isDomainRune(r rune) bool {
	return r == '.' || r == '-' || unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r)
}

func isLocalRune(r rune) bool {
	return isDomainRune(r) || r == '_' || r == '%' || r == '+'
}

// emailSpans finds every address in text by its '@', reading the local
// part and the domain as Unicode, so maría.lópez@correo.example and
// user@例え.jp are found whole. The domain must hold a dot with a label
// on both sides.
func emailSpans(text []byte) []core.Span {
	var out []core.Span
	for off := 0; off < len(text); {
		i := bytes.IndexByte(text[off:], '@')
		if i < 0 {
			break
		}
		at := off + i
		off = at + 1
		start, end := widenEmail(text, at, at+1)
		if start == at || end <= at+1 {
			continue
		}
		dom := text[at+1 : end]
		if dot := bytes.LastIndexByte(dom, '.'); dot <= 0 || dot == len(dom)-1 {
			continue
		}
		out = append(out, core.Span{Start: start, End: end, Kind: core.KEmail})
		off = end
	}
	return out
}
