// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package phpser

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/roadie/internal/testutil"
)

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".ser")) // #nosec G304 -- the test's own fixture
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// extraSeeds start the fuzzers on every type and reference form.
var extraSeeds = []string{
	"N;", "b:1;", "i:-3;", "d:-INF;", "d:1.5e3;", `s:3:"a"c";`, `a:2:{i:0;N;s:1:"k";s:1:"v";}`,
	`O:8:"stdClass":1:{s:1:"a";a:1:{i:0;R:2;}}`, `C:8:"stdClass":2:{"}}`, `E:7:"Foo:Bar";`,
	`a:3:{i:0;O:8:"stdClass":0:{}i:1;r:2;i:2;R:2;}`, `a:2:{s:1:"5";s:1:"x";i:5;s:1:"y";}`,
}

func fixtureSeeds(t testing.TB) [][]byte {
	t.Helper()
	names, err := filepath.Glob(filepath.Join("testdata", "*.ser"))
	if err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	for _, n := range names {
		out = append(out, readFixture(t, strings.TrimSuffix(filepath.Base(n), ".ser")))
	}
	return out
}

type leaf struct {
	path, leaf string
	key        bool
}

func leavesOf(t *testing.T, b []byte) []leaf {
	t.Helper()
	var got []leaf
	if err := Leaves(b, func(path string, l []byte, isKey bool) {
		got = append(got, leaf{path, string(l), isKey})
	}); err != nil {
		t.Fatalf("Leaves: %v", err)
	}
	return got
}

// The fixtures come from real PHP (gen.php); each is valid or not as PHP
// says and walks to the leaves listed.
func TestFixtures(t *testing.T) {
	cases := []struct {
		name   string
		valid  bool
		leaves []leaf
	}{
		{"multibyte", true, []leaf{{"", "Zoë Ångström", false}}},
		{"nul-props", true, []leaf{
			{"name", "name", true}, {"name", "Jane Roe", false},
			{"email", "\x00*\x00email", true}, {"email", "jane@example.invalid", false},
			{"phone", "\x00Customer\x00phone", true}, {"phone", "+1 555 0100", false},
		}},
		{"nested", true, []leaf{
			{"a", "a", true}, {"a.b", "b", true}, {"a.b.c", "c", true}, {"a.b.c", "deep@example.invalid", false},
			{"a.b.n", "n", true}, {"list", "list", true}, {"list[3]", "x", false},
		}},
		{"enum", true, []leaf{{"suit", "suit", true}, {"name", "name", true}, {"name", "card", false}}},
		{"refs", true, []leaf{
			{"one", "one", true}, {"one.email", "email", true}, {"one.email", "shared@example.invalid", false},
			{"two", "two", true}, {"list", "list", true}, {"list[0]", "first", false}, {"list[1]", "second", false}, {"again", "again", true},
		}},
		{"inner", true, []leaf{{"data", "data", true}, {"data", `a:1:{s:5:"email";s:21:"inner@example.invalid";}`, false}}},
		{"floats", true, nil},
		{"leading-zero", true, []leaf{{"[0]", "abc", false}}},
		{"plus-int", true, nil},
		{"invalid-bool-key", false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := readFixture(t, c.name)
			if got := Valid(b); got != c.valid {
				t.Fatalf("Valid = %v, want %v", got, c.valid)
			}
			if !c.valid {
				_, _, err := Rewrite(b, nil)
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("Rewrite error %v, want ErrInvalid", err)
				}
				return
			}
			got := leavesOf(t, b)
			if len(got) != len(c.leaves) {
				t.Fatalf("leaves %v, want %v", got, c.leaves)
			}
			for i := range got {
				if got[i] != c.leaves[i] {
					t.Errorf("leaf %d = %+q, want %+q", i, got[i].path+"|"+got[i].leaf, c.leaves[i].path+"|"+c.leaves[i].leaf)
				}
			}
		})
	}
}

// gen.php still writes the committed fixtures byte for byte.
func TestGenerated(t *testing.T) {
	php := testutil.RequirePHP(t)
	dir := t.TempDir()
	cmd := exec.Command(php, "-n", filepath.Join("testdata", "gen.php"), dir) // #nosec G204 -- the test's own script
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("gen.php: %v\n%s", err, out)
	}
	names, _ := filepath.Glob(filepath.Join(dir, "*.ser"))
	if len(names) < 10 {
		t.Fatalf("gen.php wrote %d fixtures", len(names))
	}
	for _, n := range names {
		got, _ := os.ReadFile(n) // #nosec G304 -- written by the test
		if want := readFixture(t, strings.TrimSuffix(filepath.Base(n), ".ser")); !bytes.Equal(got, want) {
			t.Errorf("%s: gen.php writes %q, committed %q", filepath.Base(n), got, want)
		}
	}
}

func replaceAll(to string) LeafFunc {
	return func(_ string, _ []byte, isKey bool) ([]byte, bool, error) {
		if isKey {
			return nil, false, nil
		}
		return []byte(to), true, nil
	}
}

func TestRewrite(t *testing.T) {
	cases := []struct{ in, to, want string }{
		// lengths are bytes: Zoë Ångström is 15 bytes and 12 runes
		{`s:1:"x";`, "Zoë Ångström", `s:15:"Zoë Ångström";`},
		{`s:15:"Zoë Ångström";`, "x", `s:1:"x";`},
		// untouched bytes stay as they were, leading zeros included
		{`a:01:{i:+5;s:03:"abc";}`, "toolong", `a:01:{i:+5;s:7:"toolong";}`},
		{"O:8:\"Customer\":1:{s:8:\"\x00*\x00email\";s:1:\"a\";}", "bb", "O:8:\"Customer\":1:{s:8:\"\x00*\x00email\";s:2:\"bb\";}"},
		{`a:2:{i:0;O:8:"stdClass":1:{s:1:"e";s:1:"a";}i:1;r:2;}`, "", `a:2:{i:0;O:8:"stdClass":1:{s:1:"e";s:0:"";}i:1;r:2;}`},
		{`C:8:"stdClass":10:{s:1:"a";N;}`, "zz", `C:8:"stdClass":10:{s:1:"a";N;}`},
		{`a:1:{i:0;d:1.5;}`, "zz", `a:1:{i:0;d:1.5;}`},
	}
	for _, c := range cases {
		out, _, err := Rewrite([]byte(c.in), replaceAll(c.to))
		if err != nil {
			t.Fatalf("Rewrite(%q): %v", c.in, err)
		}
		if string(out) != c.want {
			t.Errorf("Rewrite(%q) = %q, want %q", c.in, out, c.want)
		}
		if !Valid(out) {
			t.Errorf("Rewrite(%q) is not valid: %q", c.in, out)
		}
	}
}

// A serialized string inside a serialized string is fixed inside out: the
// prototype fixed only the inner length and broke the outer one.
func TestRewriteInsideOut(t *testing.T) {
	b := readFixture(t, "inner")
	out, changed, err := Rewrite(b, func(_ string, l []byte, isKey bool) ([]byte, bool, error) {
		if isKey || !Valid(l) {
			return nil, false, nil
		}
		return Rewrite(l, replaceAll("customer-0001@example.invalid"))
	})
	if err != nil || !changed {
		t.Fatalf("Rewrite: changed %v, %v", changed, err)
	}
	want := `a:1:{s:4:"data";s:55:"a:1:{s:5:"email";s:29:"customer-0001@example.invalid";}";}`
	if string(out) != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	if !Valid(out) {
		t.Fatal("outer value invalid")
	}
}

func TestRewriteKeysAndPaths(t *testing.T) {
	in := `a:2:{s:3:"a.b";a:1:{i:7;s:1:"v";}s:1:"k";s:1:"w";}`
	var paths []string
	out, changed, err := Rewrite([]byte(in), func(path string, l []byte, isKey bool) ([]byte, bool, error) {
		paths = append(paths, path)
		if isKey && string(l) == "k" {
			return []byte("key"), true, nil
		}
		return nil, false, nil
	})
	if err != nil || !changed {
		t.Fatalf("changed %v, %v", changed, err)
	}
	if want := `a:2:{s:3:"a.b";a:1:{i:7;s:1:"v";}s:3:"key";s:1:"w";}`; string(out) != want {
		t.Errorf("got %q, want %q", out, want)
	}
	if got, want := strings.Join(paths, " "), `a\.b a\.b[7] k k`; got != want {
		t.Errorf("paths %q, want %q", got, want)
	}
}

func TestRewriteUnchangedIsInput(t *testing.T) {
	b := readFixture(t, "nested")
	out, changed, err := Rewrite(b, func(string, []byte, bool) ([]byte, bool, error) { return nil, false, nil })
	if err != nil || changed || !bytes.Equal(out, b) {
		t.Fatalf("identity rewrite: %q %v %v", out, changed, err)
	}
}

func TestRewriteFnError(t *testing.T) {
	boom := errors.New("boom")
	_, _, err := Rewrite([]byte(`s:1:"a";`), func(string, []byte, bool) ([]byte, bool, error) { return nil, false, boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err %v, want boom", err)
	}
}

func TestInvalid(t *testing.T) {
	for _, in := range []string{"", `s:1:"a"`, "N;x", `S:1:"a";`, strings.Repeat("a:1:{i:0;", 513) + "N;" + strings.Repeat("}", 513)} {
		_, _, err := Rewrite([]byte(in), nil)
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("Rewrite(%.20q): %v, want ErrInvalid", in, err)
		}
		if err := Leaves([]byte(in), func(string, []byte, bool) {}); !errors.Is(err, ErrInvalid) {
			t.Errorf("Leaves(%.20q): %v, want ErrInvalid", in, err)
		}
	}
	deep := strings.Repeat("a:1:{i:0;", 512) + "N;" + strings.Repeat("}", 512)
	if !Valid([]byte(deep)) {
		t.Error("512 levels must be valid")
	}
	if msg := Valid([]byte("N;x")); msg {
		t.Error("trailing byte accepted")
	}
	_, _, err := Rewrite([]byte("a:1:{b:0;N;}"), nil)
	if err == nil || !strings.Contains(err.Error(), "at byte 5") {
		t.Errorf("error %v names no position", err)
	}
}

// A declared length far beyond the input is refused before anything is
// read or allocated.
func TestHugeLengths(t *testing.T) {
	for _, in := range []string{`s:999999999999:"a";`, "a:999999999999:{}", `O:8:"stdClass":999999999999:{}`, `C:8:"stdClass":999999999999:{}`, `O:999999999:"a":0:{}`} {
		allocs := testing.AllocsPerRun(10, func() { Valid([]byte(in)) })
		if Valid([]byte(in)) || allocs > 2 {
			t.Errorf("%s: valid or %v allocations", in, allocs)
		}
	}
}

func FuzzPHPSerializedRoundTrip(f *testing.F) {
	for _, b := range fixtureSeeds(f) {
		f.Add(b)
	}
	for _, s := range extraSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		identity := func(_ string, l []byte, _ bool) ([]byte, bool, error) { return l, true, nil }
		out, _, err := Rewrite(b, identity)
		if !Valid(b) {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid input gave %v", err)
			}
			return
		}
		if err != nil {
			t.Fatalf("valid input: %v", err)
		}
		if !bytes.Equal(out, b) {
			t.Fatalf("identity rewrite differs: %q to %q", b, out)
		}
		same, changed, err := Rewrite(b, func(string, []byte, bool) ([]byte, bool, error) { return nil, false, nil })
		if err != nil || changed || !bytes.Equal(same, b) {
			t.Fatalf("unchanged rewrite differs: %q", same)
		}
	})
}

// shape is b with every string value (and every key when keys) made the
// same, so two values with the same types, counts and other keys give the
// same shape. Two passes, since a leaf already equal to the first marker
// keeps its original length prefix.
func shape(t *testing.T, b []byte, keys bool) []byte {
	t.Helper()
	for _, mark := range []string{"", "x"} {
		var err error
		b, _, err = Rewrite(b, func(_ string, _ []byte, isKey bool) ([]byte, bool, error) {
			if isKey && !keys {
				return nil, false, nil
			}
			return []byte(mark), true, nil
		})
		if err != nil {
			t.Fatalf("shape: %v", err)
		}
	}
	return b
}

func FuzzPHPSerializedRewrite(f *testing.F) {
	for _, b := range fixtureSeeds(f) {
		f.Add(b, "x")
	}
	for _, s := range extraSeeds {
		f.Add([]byte(s), "Zoë")
	}
	f.Fuzz(func(t *testing.T, b []byte, extra string) {
		if !Valid(b) {
			return
		}
		// Keys only change when no reference can see the difference: a
		// renamed key can merge or split buckets that r: and R: name.
		refs := bytes.Contains(b, []byte("r:")) || bytes.Contains(b, []byte("R:"))
		out, _, err := Rewrite(b, func(_ string, l []byte, isKey bool) ([]byte, bool, error) {
			if isKey && refs {
				return nil, false, nil
			}
			n := append([]byte("k"), l...)
			if !isKey {
				n = append(n, extra...)
			}
			return append(n, '#'), true, nil
		})
		if err != nil {
			t.Fatalf("Rewrite: %v", err)
		}
		if !Valid(out) {
			t.Fatalf("rewrite of %q is invalid: %q", b, out)
		}
		if !bytes.Equal(shape(t, b, !refs), shape(t, out, !refs)) {
			t.Fatalf("shape changed: %q to %q", b, out)
		}
	})
}

func benchInput() []byte {
	var b bytes.Buffer
	b.WriteString("a:1000:{")
	for i := range 1000 {
		b.WriteString("i:")
		b.WriteString(strings.Repeat("1", 1+i%3))
		b.WriteString(`;a:4:{s:5:"email";s:24:"customer@example.invalid";s:4:"name";s:8:"Jane Roe";s:5:"total";d:19.99;s:4:"note";s:60:"`)
		b.WriteString(strings.Repeat("n", 60))
		b.WriteString(`";}`)
	}
	b.WriteString("}")
	return b.Bytes()
}

func BenchmarkRewrite(b *testing.B) {
	in := benchInput()
	if !Valid(in) {
		b.Fatal("bench input invalid")
	}
	fn := func(_ string, l []byte, isKey bool) ([]byte, bool, error) {
		if !isKey && bytes.IndexByte(l, '@') >= 0 {
			return []byte("u0123456789abcdef@example.invalid"), true, nil
		}
		return nil, false, nil
	}
	b.SetBytes(int64(len(in)))
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := Rewrite(in, fn); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValid(b *testing.B) {
	in := benchInput()
	b.SetBytes(int64(len(in)))
	for b.Loop() {
		Valid(in)
	}
}

// Allocation guards: Valid allocates nothing and a Rewrite or Leaves
// that changes nothing allocates for the leaf paths only, at most one
// per string it hands to fn.
func TestAllocs(t *testing.T) {
	in := benchInput()
	strs := 0
	if err := Leaves(in, func(string, []byte, bool) { strs++ }); err != nil {
		t.Fatal(err)
	}
	if n := testing.AllocsPerRun(10, func() { Valid(in) }); n != 0 {
		t.Errorf("Valid: %v allocations, want 0", n)
	}
	none := func(string, []byte, bool) ([]byte, bool, error) { return nil, false, nil }
	if n := testing.AllocsPerRun(10, func() { _, _, _ = Rewrite(in, none) }); n > float64(strs)+16 {
		t.Errorf("Rewrite that changes nothing: %v allocations for %d strings", n, strs)
	}
	if n := testing.AllocsPerRun(10, func() { _ = Leaves(in, func(string, []byte, bool) {}) }); n > float64(strs)+16 {
		t.Errorf("Leaves: %v allocations for %d strings", n, strs)
	}
}
