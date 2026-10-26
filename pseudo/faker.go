// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package pseudo

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"hash/maphash"
	"strings"
	"sync"

	"github.com/RhythmShahriar/roadie/core"
)

// The Faker defaults.
const (
	DefaultEmailDomain = "example.invalid"
	DefaultLocale      = "en"
	DefaultCacheSize   = 65536
	maxCacheSize       = 1 << 24 // the cache links its entries by int32
)

// The phone styles.
const (
	PhoneEcho    = "echo"    // +1 555 01x xxxx, the range Echo treats as test numbers
	PhoneCountry = "country" // UK numbers to +44 7700 900xxx, others as echo
)

// uniqueKinds are the kinds whose fakes stand in for unique columns
// (user_login, user_email, user_nicename) and so must never collide.
var uniqueKinds = map[core.FakeKind]bool{
	core.KEmail: true, core.KUsername: true, core.KNicename: true, core.KURL: true,
}

// suffixKinds are the list kinds that can still be made unique, with a
// " <8 hex>" suffix, when FakeOpts.Unique asks for it. Other kinds
// (phones, IPs, dates, postcodes) have a fixed shape and ignore Unique.
var suffixKinds = map[core.FakeKind]bool{
	core.KFirstName: true, core.KLastName: true, core.KFullName: true, core.KStreet: true,
	core.KAddress: true, core.KCity: true, core.KCompany: true,
}

// faker is the core.Faker NewFaker returns. It is for one goroutine.
type faker struct {
	key   Key
	opt   Options
	mac   hash.Hash // HMAC-SHA256 keyed by key, reset for every value
	in    []byte    // scratch: kind || 0x00 || normalised value
	norm  []byte    // scratch: the normalised value
	sum   []byte    // scratch: the last HMAC
	cache *lru
	uniq  *uniqueSet // shared by every clone
}

func newFaker(k Key, o Options) (core.Faker, error) {
	o.EmailDomain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(o.EmailDomain), "."))
	if o.EmailDomain == "" {
		o.EmailDomain = DefaultEmailDomain
	}
	if !validDomain(o.EmailDomain) || !core.ReservedEmailDomain(o.EmailDomain) {
		return nil, core.Errorf(core.ErrConfig, 2,
			"pseudonymise.email_domain %q isn't reserved: use example.com, example.net, example.org or a name ending in .example, .invalid, .test, .localhost or .local",
			o.EmailDomain)
	}
	if o.Locale == "" {
		o.Locale = DefaultLocale
	}
	if !validLocale(o.Locale) {
		return nil, core.Errorf(core.ErrConfig, 2, "pseudonymise.locale %q isn't one of %s", o.Locale, strings.Join(Locales, ", "))
	}
	if o.PhoneStyle == "" {
		o.PhoneStyle = PhoneEcho
	}
	if o.PhoneStyle != PhoneEcho && o.PhoneStyle != PhoneCountry {
		return nil, core.Errorf(core.ErrConfig, 2, "pseudonymise.phone_style %q isn't echo or country", o.PhoneStyle)
	}
	if o.CacheSize <= 0 {
		o.CacheSize = DefaultCacheSize
	}
	o.CacheSize = min(o.CacheSize, maxCacheSize)
	loadLists()
	f := &faker{key: k, opt: o, uniq: newUniqueSet()}
	f.init()
	return f, nil
}

// validDomain reports whether d is a host name of ASCII letters, digits,
// hyphens and dots, so it can end a fake email.
func validDomain(d string) bool {
	if d == "" || len(d) > 253 || d[0] == '.' || strings.Contains(d, "..") {
		return false
	}
	for i := 0; i < len(d); i++ {
		c := d[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

// init gives f its own HMAC state and cache.
func (f *faker) init() {
	f.mac = hmac.New(sha256.New, f.key[:])
	f.cache = newLRU(f.opt.CacheSize)
}

// Clone returns a faker with the same key, options and unique set and
// its own HMAC state and cache, for another worker.
func (f *faker) Clone() core.Faker {
	c := &faker{key: f.key, opt: f.opt, uniq: f.uniq}
	c.init()
	return c
}

// Fake returns the fake of original as kind, fitted to o.
func (f *faker) Fake(kind core.FakeKind, original []byte, o core.FakeOpts) ([]byte, error) {
	if !knownKind(kind) || kind == core.KText {
		return nil, core.Errorf(core.ErrConfig, 2, "no fake for the kind %q; text is scrubbed, not faked", kind)
	}
	if len(bytes.TrimSpace(original)) == 0 {
		return bytes.Clone(original), nil // nothing personal in an empty value
	}
	// The kind the hash is keyed on: a login or nicename that is an email
	// address is keyed as that email, so a login equal to the email gets
	// the fake email while a nicename equal to the login gets its fake.
	hk := kind
	switch kind {
	case core.KNicename:
		hk = core.KUsername
		if looksLikeEmail(original) {
			hk = core.KEmail
		}
	case core.KUsername:
		if looksLikeEmail(original) {
			hk = core.KEmail
		}
	}
	f.norm = appendNormalise(f.norm[:0], hk, original)
	n := f.norm
	ck := f.cache.key(kind, n, o)
	if out, ok := f.cache.get(ck); ok {
		return bytes.Clone(out), nil
	}
	f.in = append(append(append(f.in[:0], hk...), 0), n...)
	f.mac.Reset()
	f.mac.Write(f.in)
	f.sum = f.mac.Sum(f.sum[:0])
	out, err := f.format(kind, hk, f.sum, original, o)
	if err != nil {
		return nil, err
	}
	if uniqueKinds[kind] || o.Unique && suffixKinds[kind] {
		if err := f.uniq.add(kind, out, f.sum); err != nil {
			return nil, err
		}
	}
	// The cache keeps its own copy, so out, fresh from format, is the
	// caller's without a second copy.
	f.cache.put(ck, out)
	return out, nil
}

// knownKind reports whether kind is one of core.FakeKinds.
func knownKind(kind core.FakeKind) bool {
	for _, k := range core.FakeKinds() {
		if k == kind {
			return true
		}
	}
	return false
}

// looksLikeEmail reports whether b has one '@' with text before it and a
// dot after it, so a login holding an address is treated as one.
func looksLikeEmail(b []byte) bool {
	b = bytes.TrimSpace(b)
	at := bytes.IndexByte(b, '@')
	return at > 0 && bytes.Count(b, []byte("@")) == 1 && bytes.IndexByte(b[at+1:], '.') > 0
}

// uniqueSet is the per-run set of fakes for unique columns, sharded so
// workers rarely wait on each other. It maps a hash of kind and fake to
// 8 bytes of the HMAC of the original, so a second original with the
// same fake is told from a repeat of the first.
type uniqueSet struct {
	seed   maphash.Seed
	shards [64]struct {
		mu sync.Mutex
		m  map[uint64]uint64
	}
}

func newUniqueSet() *uniqueSet {
	u := &uniqueSet{seed: maphash.MakeSeed()}
	for i := range u.shards {
		u.shards[i].m = map[uint64]uint64{}
	}
	return u
}

// add records fake for the original sum stands for; a different original
// already holding fake is a fake-collision error (exit 3).
func (u *uniqueSet) add(kind core.FakeKind, fake, sum []byte) error {
	var h maphash.Hash
	h.SetSeed(u.seed)
	_, _ = h.WriteString(string(kind))
	_ = h.WriteByte(0)
	_, _ = h.Write(fake)
	k := h.Sum64()
	id := binary.BigEndian.Uint64(sum[8:16])
	s := &u.shards[k%uint64(len(u.shards))]
	s.mu.Lock()
	defer s.mu.Unlock()
	if prev, ok := s.m[k]; ok {
		if prev != id {
			return collision(kind)
		}
		return nil
	}
	s.m[k] = id
	return nil
}

// collision is the fake-collision error of PRD 17 for kind.
func collision(kind core.FakeKind) error {
	what := map[core.FakeKind]string{
		core.KEmail: "emails", core.KUsername: "usernames", core.KNicename: "nicenames", core.KURL: "URLs",
	}[kind]
	if what == "" {
		what = strings.ReplaceAll(string(kind), "_", " ") + " values"
	}
	return core.Errorf(core.ErrFakeCollision, 3,
		"fake-collision: two different %s got the same fake in this run. Run again (a new key fixes it); this should happen about once in 100 million runs.", what)
}

// lru is a bounded least-recently-used cache of fakes for one worker.
// Entries live in one slice linked by index and an evicted entry's
// buffer holds the next value, so a full cache allocates nothing.
type lru struct {
	seed       maphash.Seed
	scratch    []byte // the bytes of the last key, which get and put use
	max        int
	idx        map[uint64]int32
	ents       []lruEntry
	head, tail int32 // most and least recently used; -1 when empty
}

// lruEntry is one cached fake. buf holds the whole key, not only its
// hash, so a hash collision is a miss and never the wrong fake; keeping
// kind and options in those bytes keeps the entry small.
type lruEntry struct {
	key        uint64
	buf        []byte // the key's bytes, then the fake
	klen       int32  // the length of the key's bytes in buf
	prev, next int32
}

func newLRU(maxEntries int) *lru {
	return &lru{seed: maphash.MakeSeed(), max: maxEntries, idx: make(map[uint64]int32), head: -1, tail: -1}
}

// key hashes what a fake depends on: kind, normalised value and options.
// The bytes it hashes are kept for get and put to compare, so they spell
// each input with its length and two different inputs never share them.
func (c *lru) key(kind core.FakeKind, n []byte, o core.FakeOpts) uint64 {
	b := append(append(c.scratch[:0], kind...), 0) // a kind holds no NUL
	b = binary.AppendUvarint(b, uint64(len(n)))
	b = append(b, n...)
	b = binary.LittleEndian.AppendUint64(b, uint64(o.MaxLen)) // #nosec G115 -- compared, never read back
	unique := byte(0)
	if o.Unique {
		unique = 1
	}
	b = append(b, unique)
	b = binary.AppendUvarint(b, uint64(len(o.Country)))
	b = append(append(b, o.Country...), o.Locale...)
	c.scratch = b
	return maphash.Bytes(c.seed, b)
}

// get returns the cached fake for the key k was made from by the last
// key call; a hash match with other inputs is a miss.
func (c *lru) get(k uint64) ([]byte, bool) {
	i, ok := c.idx[k]
	if !ok {
		return nil, false
	}
	e := &c.ents[i]
	if !bytes.Equal(e.buf[:e.klen], c.scratch) {
		return nil, false
	}
	c.touch(i)
	return e.buf[e.klen:], true
}

// put stores fake for the key k was made from by the last key call,
// evicting the least recently used entry when full.
func (c *lru) put(k uint64, fake []byte) {
	if i, ok := c.idx[k]; ok {
		c.ents[i].set(c.scratch, fake)
		c.touch(i)
		return
	}
	var i int32
	if len(c.ents) < c.max {
		c.ents = append(c.ents, lruEntry{prev: -1, next: -1})
		i = int32(len(c.ents) - 1) // #nosec G115 -- bounded by max, an int that fits the cache
	} else {
		i = c.tail
		c.unlink(i)
		delete(c.idx, c.ents[i].key)
	}
	e := &c.ents[i]
	e.key, e.prev, e.next = k, -1, -1
	e.set(c.scratch, fake)
	c.idx[k] = i
	c.pushFront(i)
}

// set fills e, reusing the buffer of the value it held before.
func (e *lruEntry) set(key, fake []byte) {
	e.klen = int32(len(key)) // #nosec G115 -- a key is a value of one column plus a few bytes
	e.buf = append(append(e.buf[:0], key...), fake...)
}

func (c *lru) touch(i int32) {
	if c.head == i {
		return
	}
	c.unlink(i)
	c.pushFront(i)
}

func (c *lru) unlink(i int32) {
	e := &c.ents[i]
	if e.prev >= 0 {
		c.ents[e.prev].next = e.next
	} else {
		c.head = e.next
	}
	if e.next >= 0 {
		c.ents[e.next].prev = e.prev
	} else {
		c.tail = e.prev
	}
	e.prev, e.next = -1, -1
}

func (c *lru) pushFront(i int32) {
	e := &c.ents[i]
	e.prev, e.next = -1, c.head
	if c.head >= 0 {
		c.ents[c.head].prev = i
	}
	c.head = i
	if c.tail < 0 {
		c.tail = i
	}
}
