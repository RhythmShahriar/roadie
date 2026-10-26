// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import "github.com/RhythmShahriar/roadie/core"

// ScrubSpans replaces the given spans of text by their fakes, as Scrub
// does, and nothing else: no addresses of its own finding and no names.
// It is for text no rule covers, where only what a detector reports as
// personal is replaced and an address at a reserved domain stays. An
// email span is widened over the Unicode letters around it and may be
// written with an encoded at sign (%40, &#64;); its fake goes back in the
// same form.
func ScrubSpans(text []byte, spans []core.Span, f core.Faker) ([]byte, int) {
	if len(spans) == 0 {
		return text, 0
	}
	return scrubFound(text, spans, nil, f)
}
