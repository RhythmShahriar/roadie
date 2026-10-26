// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/RhythmShahriar/roadie/core"
)

// localeLists returns the lists of loc, or of en when loc is unknown.
func localeLists(loc string) *locale {
	loadLists()
	if l, ok := lists.byLocale[loc]; ok {
		return l
	}
	return lists.byLocale["en"]
}

// format turns the HMAC sum into the fake of kind; hk is the kind the
// hash was keyed on (an email for a login that is an address).
func (f *faker) format(kind, hk core.FakeKind, sum, original []byte, o core.FakeOpts) ([]byte, error) {
	loc := localeLists(f.opt.Locale)
	if o.Locale != "" && validLocale(o.Locale) {
		loc = localeLists(o.Locale)
	}
	switch kind {
	case core.KEmail:
		return f.email(kind, sum, o.MaxLen)
	case core.KUsername:
		if hk == core.KEmail {
			return f.email(kind, sum, o.MaxLen)
		}
		return login(kind, sum, o.MaxLen)
	case core.KNicename:
		limit := o.MaxLen
		if limit <= 0 || limit > maxNicename {
			limit = maxNicename
		}
		return login(kind, sum, limit) // already lower case
	case core.KURL:
		return fakeURL(sum, o.MaxLen)
	case core.KPhone:
		return f.phone(sum, original, o)
	case core.KIP:
		return fakeIP(sum, original, o.MaxLen)
	case core.KPostcode:
		return fakePostcode(sum, original), nil
	case core.KDOB:
		return fakeDOB(sum, original, o.Country), nil
	case core.KUserAgent:
		return cut([]byte(userAgent), o.MaxLen), nil
	}
	// The list kinds.
	var s string
	switch kind {
	case core.KFirstName:
		s = pick(loc.first, sum, 0)
	case core.KLastName:
		s = pick(loc.last, sum, 8)
	case core.KFullName:
		s = pick(loc.first, sum, 0) + " " + pick(loc.last, sum, 8)
	case core.KStreet, core.KAddress:
		s = street(loc, sum)
	case core.KCity:
		s = pick(loc.cities, sum, 0)
	case core.KCompany:
		s = pick(loc.companies, sum, 0) + " " + pick(loc.companySuffixes, sum, 8)
	}
	if !o.Unique {
		return cutWord(s, o.MaxLen), nil
	}
	suffix := " " + hex.EncodeToString(sum[24:28])
	if o.MaxLen > 0 && o.MaxLen <= len(suffix) {
		return nil, tooShort(kind, len(suffix)+1, o.MaxLen)
	}
	limit := o.MaxLen
	if limit > 0 {
		limit -= len(suffix)
	}
	return append(cutWord(s, limit), suffix...), nil
}

// tooShort is the error for a column too narrow for any fake of kind.
func tooShort(kind core.FakeKind, need, have int) error {
	return core.Errorf(core.ErrConfig, 2,
		"a fake %s needs at least %d bytes but the column holds %d; blank the column",
		strings.ReplaceAll(string(kind), "_", " "), need, have)
}

// tooShortEmail is tooShort for an email-shaped fake, where the email
// domain takes part of the room and so a shorter one can help.
func tooShortEmail(kind core.FakeKind, need, have int) error {
	return core.Errorf(core.ErrConfig, 2,
		"a fake %s needs at least %d bytes but the column holds %d; blank the column or use a shorter pseudonymise.email_domain",
		strings.ReplaceAll(string(kind), "_", " "), need, have)
}

// pick chooses an entry of list by the 8 bytes of sum at off.
func pick(list []string, sum []byte, off int) string {
	return list[binary.BigEndian.Uint64(sum[off:off+8])%uint64(len(list))]
}

// cut shortens b to limit bytes when limit is set.
func cut(b []byte, limit int) []byte {
	if limit > 0 && len(b) > limit {
		return b[:limit]
	}
	return b
}

// cutWord shortens the ASCII s to limit bytes and drops the space it may
// end on.
func cutWord(s string, limit int) []byte {
	if limit > 0 && len(s) > limit {
		s = strings.TrimRight(s[:limit], " ")
	}
	return []byte(s)
}

// email is u<16 hex>@<domain>, with fewer hex digits (never under 12) when
// the column is narrow.
func (f *faker) email(kind core.FakeKind, sum []byte, limit int) ([]byte, error) {
	n := hexLen
	if fixed := 2 + len(f.opt.EmailDomain); limit > 0 && fixed+n > limit {
		n = limit - fixed
		if n < minUniqueHex {
			return nil, tooShortEmail(kind, fixed+minUniqueHex, limit)
		}
	}
	out := make([]byte, 0, 2+n+len(f.opt.EmailDomain))
	out = append(out, 'u')
	out = hex.AppendEncode(out, sum[:8])[:1+n]
	out = append(out, '@')
	return append(out, f.opt.EmailDomain...), nil
}

// login is user_<16 hex>, shortened like email.
func login(kind core.FakeKind, sum []byte, limit int) ([]byte, error) {
	const prefix = "user_"
	n := hexLen
	if limit > 0 && len(prefix)+n > limit {
		n = limit - len(prefix)
		if n < minUniqueHex {
			return nil, tooShort(kind, len(prefix)+minUniqueHex, limit)
		}
	}
	return hex.AppendEncode([]byte(prefix), sum[:8])[:len(prefix)+n], nil
}

// fakeURL is https://u<12 hex>.example.invalid/; a narrow column loses
// the closing slash. The hex never gets shorter: URLs are a unique kind
// and fewer digits would collide within a few hundred rows.
func fakeURL(sum []byte, limit int) ([]byte, error) {
	const head, tail = "https://u", ".example.invalid"
	slash := true
	if limit > 0 && len(head)+urlHexLen+len(tail)+1 > limit {
		slash = false
		if len(head)+urlHexLen+len(tail) > limit {
			return nil, tooShort(core.KURL, len(head)+urlHexLen+len(tail), limit)
		}
	}
	out := hex.AppendEncode([]byte(head), sum[:urlHexLen/2])
	out = append(out, tail...)
	if slash {
		out = append(out, '/')
	}
	return out, nil
}

// phone is +1 555 01x xxxx (echo) or, in the country style for a UK
// number, +44 7700 900xxx. A narrow column drops the spaces.
func (f *faker) phone(sum, original []byte, o core.FakeOpts) ([]byte, error) {
	var s string
	if f.opt.PhoneStyle == PhoneCountry && ukPhone(original, o.Country) {
		s = "+44 7700 900" + pad(binary.BigEndian.Uint32(sum[:4])%1000, 3)
	} else {
		d := pad(binary.BigEndian.Uint32(sum[:4])%100000, 5)
		s = "+1 555 01" + d[:1] + " " + d[1:]
	}
	if o.MaxLen > 0 && len(s) > o.MaxLen {
		s = strings.ReplaceAll(s, " ", "")
		if len(s) > o.MaxLen {
			return nil, tooShort(core.KPhone, len(s), o.MaxLen)
		}
	}
	return []byte(s), nil
}

// ukPhone reports whether a number is British: +44, 0044 or a national
// 07 mobile, or the row's country is GB.
func ukPhone(original []byte, country string) bool {
	n := normPhone(bytes.TrimSpace(original))
	if c := strings.ToUpper(strings.TrimSpace(country)); c == "GB" || c == "UK" {
		return true
	}
	return bytes.HasPrefix(n, []byte("+44")) || bytes.HasPrefix(n, []byte("0044")) || bytes.HasPrefix(n, []byte("07"))
}

// pad writes n with leading zeros to width digits.
func pad(n uint32, width int) string {
	s := strconv.FormatUint(uint64(n), 10)
	return strings.Repeat("0", width-len(s)) + s
}

// docV4 are the three RFC 5737 documentation /24s.
var docV4 = [3][3]byte{{192, 0, 2}, {198, 51, 100}, {203, 0, 113}}

// fakeIP maps IPv4 (and IPv4-mapped IPv6) to a documentation /24 and IPv6
// to 2001:db8::/32. A column too narrow for the IPv6 form gets IPv4.
func fakeIP(sum, original []byte, limit int) ([]byte, error) {
	ip, err := netip.ParseAddr(string(bytes.TrimSpace(original)))
	if err == nil && ip.Unmap().Is6() {
		var a [16]byte
		a[0], a[1], a[2], a[3] = 0x20, 0x01, 0x0d, 0xb8
		copy(a[4:], sum[2:14])
		if s := netip.AddrFrom16(a).String(); limit <= 0 || len(s) <= limit {
			return []byte(s), nil
		}
	}
	b := docV4[sum[0]%3]
	s := netip.AddrFrom4([4]byte{b[0], b[1], b[2], 1 + sum[1]%254}).String()
	if limit > 0 && len(s) > limit {
		return nil, tooShort(core.KIP, len(s), limit)
	}
	return []byte(s), nil
}

// stream is an endless run of bytes derived from sum, for fakes longer
// than one hash.
type stream struct {
	sum   []byte
	block []byte
	n     int
}

func (s *stream) next() byte {
	if s.n%sha256.Size == 0 {
		if s.n == 0 {
			s.block = s.sum
		} else {
			h := sha256.New()
			h.Write(s.sum)
			h.Write([]byte{byte(s.n / sha256.Size)}) // #nosec G115 -- any counter bits will do
			s.block = h.Sum(nil)
		}
	}
	b := s.block[s.n%sha256.Size]
	s.n++
	return b
}

// fakePostcode keeps the original's shape: digits become digits and
// letters letters of the same case; everything else stays. The fake
// never equals the original.
func fakePostcode(sum, original []byte) []byte {
	in := bytes.TrimSpace(original)
	out := shaped(sum, in, true)
	if bytes.Equal(out, in) {
		for i, c := range out {
			if bump(&out[i], c) {
				break
			}
		}
	}
	return out
}

// shaped replaces digits (and letters when letters is set) of in with
// ones derived from sum.
func shaped(sum, in []byte, letters bool) []byte {
	st := stream{sum: sum}
	out := make([]byte, len(in))
	for i, c := range in {
		switch {
		case c >= '0' && c <= '9':
			out[i] = '0' + st.next()%10
		case letters && c >= 'A' && c <= 'Z':
			out[i] = 'A' + st.next()%26
		case letters && c >= 'a' && c <= 'z':
			out[i] = 'a' + st.next()%26
		default:
			out[i] = c
		}
	}
	return out
}

// bump moves a digit or letter to the next one of its class.
func bump(p *byte, c byte) bool {
	switch {
	case c >= '0' && c <= '9':
		*p = '0' + (c-'0'+1)%10
	case c >= 'A' && c <= 'Z':
		*p = 'A' + (c-'A'+1)%26
	case c >= 'a' && c <= 'z':
		*p = 'a' + (c-'a'+1)%26
	default:
		return false
	}
	return true
}

// dateForms are the date-of-birth spellings fakeDOB reads and writes back.
var dateForms = []string{"2006-01-02", "2006/01/02", "02.01.2006", "20060102"}

// fakeDOB shifts a date by 1 to 180 days either way, chosen by the hash,
// and keeps its year and spelling. A time after an ISO date is kept. A
// zero date stays. A value with a date's shape that isn't a real date
// keeps its year and gets a valid month and day; any other value that
// isn't a date keeps its shape with new digits. One with no digit at all ("fifth of May") is blanked:
// there is nothing to shift and it may still say the date.
func fakeDOB(sum, original []byte, country string) []byte {
	s := string(bytes.TrimSpace(original))
	shift := int(binary.BigEndian.Uint16(sum[:2])%180) + 1
	if sum[2]&1 == 1 {
		shift = -shift
	}
	if strings.Trim(s, "0-/. :") == "" {
		return []byte(s) // 0000-00-00 and friends: no date at all
	}
	if !strings.ContainsAny(s, "0123456789") {
		return []byte{}
	}
	move := func(t time.Time) time.Time {
		if n := t.AddDate(0, 0, shift); n.Year() == t.Year() {
			return n
		}
		return t.AddDate(0, 0, -shift) // one of the two directions stays in the year
	}
	for _, form := range dateForms {
		if len(s) < len(form) {
			continue
		}
		if t, err := time.Parse(form, s[:len(form)]); err == nil {
			return []byte(move(t).Format(form) + s[len(form):])
		}
	}
	if len(s) == 10 && s[2] == '/' && s[5] == '/' {
		// Day first unless the row is American or the middle field can
		// only be a day.
		form := "02/01/2006"
		if mid, err := strconv.Atoi(s[3:5]); strings.EqualFold(strings.TrimSpace(country), "US") || err == nil && mid > 12 {
			form = "01/02/2006"
		}
		if t, err := time.Parse(form, s); err == nil {
			return []byte(move(t).Format(form))
		}
	}
	// A value with a date's shape that is not a real date (1990-02-29,
	// 31/13/1990) keeps its year and gets a valid month and day, so it
	// still reads as a date in the same year.
	forms := dateForms
	if len(s) == 10 && s[2] == '/' && s[5] == '/' {
		forms = []string{"02/01/2006"}
		if strings.EqualFold(strings.TrimSpace(country), "US") {
			forms = []string{"01/02/2006"}
		}
	}
	for _, form := range forms {
		// The all-digit form needs the whole value, or any long number
		// would pass for a date.
		if len(s) < len(form) || form == "20060102" && len(s) != len(form) || !sameShape(form, s[:len(form)]) {
			continue
		}
		i := strings.Index(form, "2006")
		year, _ := strconv.Atoi(s[i : i+4])
		t := time.Date(year, time.Month(sum[3]%12+1), int(sum[4]%28)+1, 0, 0, 0, 0, time.UTC)
		return []byte(t.Format(form) + s[len(form):])
	}
	out := shaped(sum, []byte(s), false)
	if bytes.Equal(out, []byte(s)) {
		for i, c := range out {
			if c >= '0' && c <= '9' && bump(&out[i], c) {
				break
			}
		}
	}
	return out
}

// sameShape reports whether s has a digit wherever the Go layout form
// has one and the same byte everywhere else.
func sameShape(form, s string) bool {
	for i := 0; i < len(form); i++ {
		fd := form[i] >= '0' && form[i] <= '9'
		sd := s[i] >= '0' && s[i] <= '9'
		if fd != sd || !fd && form[i] != s[i] {
			return false
		}
	}
	return true
}

// street writes a fake street in the locale's shape.
func street(l *locale, sum []byte) string {
	n := strconv.Itoa(int(binary.BigEndian.Uint16(sum[16:18])%999) + 1)
	name, suf := pick(l.streets, sum, 0), pick(l.streetSuffixes, sum, 8)
	switch l.shape {
	case shapeTypeFirst:
		return n + " " + suf + " " + name
	case shapeCompound:
		return name + suf + " " + n
	case shapeTypeNumberAt:
		return suf + " " + name + " " + n
	}
	return n + " " + name + " " + suf
}
