// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package phpser

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/RhythmShahriar/roadie/internal/testutil"
)

// oracle is one long-running php -n testdata/oracle.php that answers
// whether PHP's unserialize accepts a frame of bytes.
type oracle struct {
	mu  sync.Mutex
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

func startOracle(t testing.TB) *oracle {
	t.Helper()
	php := testutil.RequirePHP(t)
	cmd := exec.Command(php, "-n", filepath.Join("testdata", "oracle.php")) // #nosec G204 -- php from PATH running the test's own script
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	o := &oracle{cmd: cmd, in: in, out: bufio.NewReader(out)}
	t.Cleanup(func() {
		_ = in.Close()
		_ = cmd.Wait()
	})
	return o
}

// valid asks PHP about b.
func (o *oracle) valid(t testing.TB, b []byte) bool {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	var head [4]byte
	binary.BigEndian.PutUint32(head[:], uint32(len(b))) //nolint:gosec // fuzz inputs are far below 4 GiB
	if _, err := o.in.Write(append(head[:], b...)); err != nil {
		t.Fatalf("oracle write: %v", err)
	}
	c, err := o.out.ReadByte()
	if err != nil {
		t.Fatalf("oracle read: %v", err)
	}
	return c == '1'
}

// goPrefix is what Go says PHP should answer: PHP accepts a valid value
// with any bytes after it. skip is true for the inputs where Roadie
// differs from PHP on purpose (S: strings, more than 512 levels); php is
// what to send PHP, with each E: enum swapped for an empty object since
// PHP refuses an enum whose class isn't loaded.
func goPrefix(b []byte) (ok, skip bool, php []byte) {
	var spans [][2]int
	p := parser{onEnum: func(s, e int) { spans = append(spans, [2]int{s, e}) }}
	n, err := p.run(b)
	if errors.Is(err, errEscaped) || errors.Is(err, errTooDeep) {
		return false, true, b
	}
	if err != nil {
		return false, false, b
	}
	if len(spans) == 0 {
		return true, false, b
	}
	var out []byte
	last := 0
	for _, s := range spans {
		out = append(out, b[last:s[0]]...)
		out = append(out, `O:8:"stdClass":0:{}`...)
		last = s[1]
	}
	out = append(out, b[last:n]...)
	return true, false, out
}

// Deliberate differences from PHP that the oracle cases name.
const (
	same     = iota
	trailing // PHP accepts the value and ignores the rest; Valid says no
	escaped  // PHP accepts S:; Valid says no
	enum     // PHP refuses an enum it can't load; Valid accepts it
)

type oracleCase struct {
	in    string
	valid bool // what Valid must answer
	diff  int
}

// oracleCases are hand-written inputs where Go and PHP must agree, apart
// from the named differences. The answers were taken from PHP 8.1 and 8.3.
var oracleCases = []oracleCase{
	// null and booleans
	{"N;", true, same}, {"N", false, same}, {"N;N;", false, trailing}, {"N; ", false, trailing}, {"N;\x00", false, trailing}, {" N;", false, same},
	{"b:0;", true, same}, {"b:1;", true, same}, {"b:2;", false, same}, {"b:01;", false, same}, {"b:;", false, same}, {"b:1", false, same},
	// integers
	{"i:0;", true, same}, {"i:+5;", true, same}, {"i:-0;", true, same}, {"i:00;", true, same}, {"i:-12;", true, same},
	{"i:99999999999999999999999;", true, same}, {"i:;", false, same}, {"i:-;", false, same}, {"i:+;", false, same}, {"i:0x1;", false, same},
	{"i:1.5;", false, same}, {"i:1", false, same}, {"i: 1;", false, same}, {"i:1 ;", false, same},
	// floats
	{"d:0;", true, same}, {"d:1e5;", true, same}, {"d:1e+5;", true, same}, {"d:1E-5;", true, same}, {"d:.5;", true, same}, {"d:5.;", true, same},
	{"d:1.e5;", true, same}, {"d:-.5;", true, same}, {"d:+.5;", true, same}, {"d:+1;", true, same}, {"d:00.5e-03;", true, same},
	{"d:NAN;", true, same}, {"d:INF;", true, same}, {"d:-INF;", true, same}, {"d:+INF;", false, same}, {"d:-NAN;", false, same},
	{"d:inf;", false, same}, {"d:.e5;", false, same}, {"d:;", false, same}, {"d:-;", false, same}, {"d:1e;", false, same}, {"d:1e-;", false, same},
	{"d:-INF ;", false, same}, {"d:.;", false, same}, {"d:1.5", false, same},
	// strings
	{`s:0:"";`, true, same}, {`s:3:"abc";`, true, same}, {`s:03:"abc";`, true, same}, {"s:15:\"Zo\xc3\xab \xc3\x85ngstr\xc3\xb6m\";", true, same},
	{"s:12:\"Zo\xc3\xab \xc3\x85ngstr\xc3\xb6m\";", false, same}, {`s:18446744073709551619:"abc";`, true, same}, {`s:+3:"abc";`, false, same},
	{`s:3:"abc"`, false, same}, {`s:3:"abc";x`, false, trailing}, {`s:4:"abc";`, false, same}, {`s:2:"abc";`, false, same}, {`s:3:"a"c";`, true, same},
	{"s:3:\"\x00\x00\x00\";", true, same}, {`s:-1:"";`, false, same}, {`s::"";`, false, same}, {`s:3:abc;`, false, same}, {`s:1:"";`, false, same},
	{`s:99:"abc";`, false, same}, {`S:3:"abc";`, false, escaped}, {`S:1:"\61";`, false, escaped},
	// arrays
	{"a:0:{}", true, same}, {"a:1:{i:0;N;}", true, same}, {"a:01:{i:0;N;}", true, same}, {"a:+1:{i:0;N;}", false, same}, {"a:-1:{}", false, same},
	{"a:18446744073709551617:{i:0;N;}", false, same}, {"a:99999999999999999999:{}", false, same}, {"a:1:{i:0;N;}x", false, trailing},
	{"a:1:{i:0;N;}}", false, trailing}, {"a:1:{b:0;N;}", false, same}, {"a:1:{d:0;N;}", false, same}, {"a:1:{N;N;}", false, same},
	{`a:1:{s:1:"a";N;`, false, same}, {`a:1:{s:1:"a";N;i:0;N;}`, false, same}, {"a:2:{i:0;N;}", false, same}, {"a:0:{", false, same},
	{"a:1:{i:0;a:1:{i:0;a:0:{}}}", true, same}, {`a:2:{s:1:"a";N;s:1:"a";N;}`, true, same}, {"a:1:{a:0:{}N;}", false, same},
	{`a:1:{i:0;s:1:"x";}`, true, same}, {"a:0:{ }", false, same}, {"a:1:{i:0;N; }", false, same}, {"a1:{}", false, same}, {"a:0{}", false, same},
	{`a:1:{S:1:"x";N;}`, false, escaped}, {`a:1:{i:0;S:1:"x";}`, false, escaped},
	// objects
	{`O:8:"stdClass":0:{}`, true, same}, {`O:0:"":0:{}`, false, same}, {`O:3:"a-b":0:{}`, false, same}, {`O:3:"1ab":0:{}`, true, same},
	{`O:3:"A\B":0:{}`, true, same}, {`O:2:"\A":0:{}`, false, same}, {`O:2:"A\":0:{}`, true, same}, {"O:1:\"\x80\":0:{}", true, same},
	{`O:1:" ":0:{}`, false, same}, {`O:1:"_":0:{}`, true, same}, {`O:8:"stdClass":+1:{s:1:"a";N;}`, true, same},
	{`O:8:"stdClass":01:{s:1:"a";N;}`, true, same}, {`O:8:"stdClass"::{}`, true, same}, {`O:8:"stdClass":-0:{}`, true, same},
	{`O:8:"stdClass":-1:{}`, false, same}, {`O:+8:"stdClass":0:{}`, false, same}, {`O:8:"stdClass":1:{i:0;N;}`, true, same},
	{`O:8:"stdClass":1:{d:0;N;}`, false, same}, {`O:8:"stdClass":1:XXs:1:"a";N;}`, false, same}, {`O:8:"stdClass":1XXs:1:"a";N;}`, true, same},
	{`O:8:"stdClass":0XX}`, true, same}, {`O:8:"stdClass":0::}`, true, same}, {`O:8:"stdClass"X0:{}`, false, same}, {`O:8:"stdClass:0:{}`, false, same},
	{`O:8:"stdClass":0:`, false, same}, {`O:8:"stdClass":0:{`, false, same}, {`O:18446744073709551624:"stdClass":0:{}`, true, same},
	{`O:8:"stdClass":99999999999999999999999:{}`, false, same}, {"O:8:\"Customer\":1:{s:8:\"\x00*\x00email\";N;}", true, same},
	{`O:22:"__PHP_Incomplete_Class":0:{}`, true, same}, {`O:8:"stdClass":1:{s:1:"a";N;}`, true, same}, {`O:8:"stdClass":1:{i:+5;N;}`, true, same},
	// custom objects
	{`C:8:"stdClass":0:{}`, true, same}, {`C:8:"stdClass":3:{abc}`, true, same}, {`C:8:"stdClass":+3:{abc}`, true, same},
	{`C:8:"stdClass":03:{abc}`, true, same}, {`C:8:"stdClass":-0:{}`, true, same}, {`C:3:"a-b":0:{}`, false, same}, {`C:8:"stdClass"::{}`, true, same},
	{`C:8:"stdClass":99999999999999999999999:{}`, false, same}, {`C:8:"stdClass":3XXabc}`, true, same}, {`C:8:"stdClass":0XX}`, true, same},
	{`C:8:"stdClass":0:{`, false, same}, {`C:8:"stdClass":1:{}`, false, same}, {`C:8:"stdClass":1:{}}`, true, same},
	{`C:8:"stdClass":0:{}x`, false, trailing}, {`a:1:{i:0;C:8:"stdClass":2:{N;}}`, true, same}, {`C:22:"__PHP_Incomplete_Class":0:{}`, true, same},
	{`C:8:"stdClass":2:{"}}`, true, same},
	// enums
	{`E:7:"Foo:Bar";`, true, enum}, {`a:1:{i:0;E:7:"Foo:Bar";}`, true, enum}, {`E:3:"F:B";`, true, enum}, {`E:3:"FB;";`, false, same},
	{`E:2:"F:";`, false, same}, {`E:2:":B";`, false, same}, {`E:7:"Foo:Bar"`, false, same}, {`a:1:{E:7:"Foo:Bar";N;}`, false, same},
	// references
	{"a:1:{i:0;R:1;}", true, same}, {"a:1:{i:0;r:1;}", false, same}, {"a:2:{i:0;N;i:1;R:2;}", true, same}, {"a:2:{i:0;N;i:1;R:3;}", false, same},
	{"r:1;", false, same}, {"R:1;", false, same}, {"a:1:{i:0;R:0;}", false, same}, {"a:2:{i:0;a:1:{i:0;N;}i:1;R:3;}", true, same},
	{`O:8:"stdClass":1:{s:1:"a";R:1;}`, true, same}, {`O:8:"stdClass":1:{s:1:"a";r:1;}`, true, same},
	{`a:2:{i:0;C:8:"stdClass":2:{N;}i:1;r:2;}`, true, same}, {`a:2:{i:0;C:8:"stdClass":2:{N;}i:1;r:3;}`, false, same},
	{`a:2:{i:0;O:1:"A":1:{s:1:"x";N;}i:1;r:3;}`, false, same}, {`a:2:{i:0;O:8:"stdClass":0:{}i:1;r:18446744073709551618;}`, true, same},
	{`a:2:{i:0;O:8:"stdClass":0:{}i:1;r:2;}`, true, same}, {`a:2:{i:0;O:8:"stdClass":0:{}i:1;R:2;}`, true, same},
	{`a:2:{i:0;N;i:0;R:2;}`, false, same}, {`a:3:{i:0;N;i:0;N;i:1;R:2;}`, true, same}, {`a:2:{i:0;a:0:{}i:1;r:2;}`, false, same},
	{`a:2:{i:0;O:8:"stdClass":0:{}i:1;r:3;}`, false, same}, {`a:3:{i:0;O:8:"stdClass":0:{}i:1;r:2;i:2;r:3;}`, true, same},
	{`a:3:{i:0;O:8:"stdClass":0:{}i:1;R:2;i:2;r:3;}`, false, same}, {`a:3:{i:0;O:8:"stdClass":0:{}i:1;R:2;i:2;R:3;}`, false, same},
	{`a:2:{i:0;N;i:1;R:00002;}`, true, same}, {`a:2:{i:0;N;i:1;R:+2;}`, false, same}, {`O:8:"stdClass":2:{s:1:"a";N;s:1:"a";R:2;}`, false, same},
	{`O:8:"stdClass":1:{s:1:"a";a:1:{i:0;R:2;}}`, true, same}, {`a:2:{s:1:"5";N;i:5;R:2;}`, false, same}, {`a:2:{s:2:"05";N;i:5;R:2;}`, true, same},
	{`a:2:{s:2:"-5";N;i:-5;R:2;}`, false, same}, {`a:2:{s:2:"-0";N;i:0;R:2;}`, true, same}, {`O:8:"stdClass":2:{s:1:"5";N;i:5;R:2;}`, false, same},
	{`a:2:{i:99999999999999999999;N;i:9223372036854775807;R:2;}`, false, same},
	{`a:2:{s:19:"9223372036854775807";N;i:9223372036854775807;R:2;}`, false, same},
	{`a:2:{s:19:"9223372036854775808";N;i:9223372036854775807;R:2;}`, true, same},
	{`a:2:{i:-99999999999999999999;N;i:-9223372036854775808;R:2;}`, false, same},
	{`a:2:{i:0;O:8:"stdClass":0:{}i:0;r:2;}`, false, same}, {`a:3:{i:0;O:8:"stdClass":0:{}i:1;N;i:0;r:2;}`, false, same},
	{`a:3:{i:0;O:8:"stdClass":0:{}i:0;N;i:1;r:2;}`, false, same}, {`a:3:{i:0;O:8:"stdClass":0:{}i:1;R:2;i:2;r:2;}`, true, same},
	{`a:1:{i:0;R:1}`, false, same}, {`a:1:{i:0;R1;}`, false, same}, {`a:1:{i:0;R:;}`, false, same},
	// the car: in a string turns reference tracking on without changing the answer
	{`a:1:{s:4:"car:";N;}`, true, same},
	// junk
	{"", false, same}, {"x", false, same}, {"{}", false, same}, {"a", false, same}, {"s", false, same}, {"O", false, same}, {"C", false, same},
	{"E", false, same}, {"r", false, same}, {"R", false, same}, {"d", false, same}, {"i", false, same}, {"b", false, same}, {";", false, same},
}

// TestOracleCases checks Valid against the answers above and against PHP
// itself when php is on PATH.
func TestOracleCases(t *testing.T) {
	if len(oracleCases) < 200 {
		t.Fatalf("%d oracle cases, want at least 200", len(oracleCases))
	}
	for _, c := range oracleCases {
		if got := Valid([]byte(c.in)); got != c.valid {
			t.Errorf("Valid(%q) = %v, want %v", c.in, got, c.valid)
		}
	}
	o := startOracle(t)
	for _, c := range oracleCases {
		phpWant := c.valid
		switch c.diff {
		case trailing, escaped:
			phpWant = true
		case enum:
			phpWant = false
		}
		if got := o.valid(t, []byte(c.in)); got != phpWant {
			t.Errorf("PHP says %v for %q, the case says %v", got, c.in, phpWant)
		}
		ok, skip, php := goPrefix([]byte(c.in))
		if !skip && o.valid(t, php) != ok {
			t.Errorf("Go and PHP disagree on %q: Go says %v", c.in, ok)
		}
	}
}

// FuzzDifferentialPHP: Roadie accepts exactly what PHP accepts (apart
// from the named differences) and every rewrite with changed leaves stays
// acceptable to PHP.
func FuzzDifferentialPHP(f *testing.F) {
	for _, c := range oracleCases {
		f.Add([]byte(c.in))
	}
	for _, b := range fixtureSeeds(f) {
		f.Add(b)
	}
	o := startOracle(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		ok, skip, php := goPrefix(b)
		if skip {
			return
		}
		if got := o.valid(t, php); got != ok {
			t.Fatalf("Go says %v, PHP says %v for %q", ok, got, b)
		}
		if !Valid(b) {
			return
		}
		out, changed, err := Rewrite(b, func(_ string, leaf []byte, isKey bool) ([]byte, bool, error) {
			if isKey {
				return nil, false, nil
			}
			return append(bytes.Clone(leaf), "\";}\x00é"...), true, nil
		})
		if err != nil {
			t.Fatalf("Rewrite of a valid input: %v", err)
		}
		if !changed {
			return
		}
		_, _, php = goPrefix(out)
		if !o.valid(t, php) {
			t.Fatalf("PHP refuses the rewrite of %q: %q", b, out)
		}
	})
}
