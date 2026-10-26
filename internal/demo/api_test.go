// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package demo

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/internal/testutil"
)

func shopBytes(t *testing.T, o Options) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := Shop(&b, o); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestShopIsDeterministic(t *testing.T) {
	a := sha256.Sum256(shopBytes(t, Options{Seed: 7}))
	b := sha256.Sum256(shopBytes(t, Options{Seed: 7}))
	if a != b {
		t.Fatal("two runs with one seed differ")
	}
	if c := sha256.Sum256(shopBytes(t, Options{Seed: 8})); c == a {
		t.Fatal("another seed gave the same dump")
	}
	// Leaky is the production data itself (see Shop).
	if l := sha256.Sum256(shopBytes(t, Options{Seed: 7, Leaky: true})); l != a {
		t.Fatal("Leaky changed the dump")
	}
}

func TestShopSizeAndSpeed(t *testing.T) {
	start := time.Now()
	b := shopBytes(t, Options{Seed: 1})
	if d := time.Since(start); d > 200*time.Millisecond && !testing.Short() && !raceEnabled {
		t.Errorf("Shop took %v, want under 200 ms", d)
	}
	if len(b) >= 2<<20 {
		t.Errorf("dump is %d bytes, want under 2 MB", len(b))
	}
}

// statements splits a dump into statements at semicolons outside quotes
// and comments: a minimal local check, independent of the dump package.
func statements(t *testing.T, dump []byte) []string {
	t.Helper()
	var out []string
	var cur strings.Builder
	quote := byte(0)
	for i := 0; i < len(dump); i++ {
		c := dump[i]
		if quote == 0 && c == '-' && i+2 < len(dump) && dump[i+1] == '-' && (dump[i+2] == ' ' || dump[i+2] == '\n') && (i == 0 || dump[i-1] == '\n') {
			j := bytes.IndexByte(dump[i:], '\n')
			if j < 0 {
				break
			}
			i += j
			continue
		}
		cur.WriteByte(c)
		switch {
		case quote != 0 && c == '\\':
			i++
			cur.WriteByte(dump[i])
		case quote != 0 && c == quote:
			quote = 0
		case quote == 0 && (c == '\'' || c == '"' || c == '`'):
			quote = c
		case quote == 0 && c == ';':
			out = append(out, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
	}
	if quote != 0 {
		t.Fatal("dump ends inside a quoted string")
	}
	if strings.TrimSpace(cur.String()) != "" {
		t.Fatalf("dump ends with an unterminated statement: %.80q", cur.String())
	}
	return out
}

func TestShopParses(t *testing.T) {
	dump := shopBytes(t, Options{Seed: 3})
	if !bytes.HasSuffix(dump, []byte("-- Dump completed on 2026-10-01 12:00:00\n")) {
		t.Error("dump lacks the mysqldump trailer")
	}
	creates, inserts := map[string]bool{}, map[string]int{}
	re := regexp.MustCompile("^(CREATE TABLE|INSERT INTO) `([a-z_]+)`")
	for _, s := range statements(t, dump) {
		if m := re.FindStringSubmatch(s); m != nil {
			if m[1] == "CREATE TABLE" {
				creates[m[2]] = true
			} else {
				inserts[m[2]]++
			}
		}
	}
	want := []string{
		"wp_options", "wp_users", "wp_usermeta", "wp_posts", "wp_postmeta", "wp_comments",
		"wp_wc_orders", "wp_wc_order_addresses", "wp_wc_orders_meta", "wp_wc_customer_lookup",
		"wp_wc_webhooks", "wp_actionscheduler_groups", "wp_actionscheduler_actions",
	}
	if len(creates) != len(want) {
		t.Errorf("%d CREATE TABLE statements, want %d", len(creates), len(want))
	}
	for _, name := range want {
		if !creates[name] || inserts[name] == 0 {
			t.Errorf("table %s: create %v, %d INSERT statements", name, creates[name], inserts[name])
		}
	}
	if inserts["wp_usermeta"] < 2 {
		t.Errorf("wp_usermeta has %d INSERT statements, want it split as mysqldump splits", inserts["wp_usermeta"])
	}
}

func TestShopContent(t *testing.T) {
	s := newShop(Options{Seed: 5})
	if len(s.customers) != DefaultCustomers || len(s.orders) != orderCount {
		t.Fatalf("%d customers and %d orders", len(s.customers), len(s.orders))
	}
	if n := len(newShop(Options{Seed: 5, Customers: 3}).customers); n != 3 {
		t.Errorf("Customers 3 gave %d", n)
	}
	emails := map[string]bool{}
	for _, c := range s.customers {
		if emails[c.email] {
			t.Errorf("customer email repeats: %s", core.MaskEmail(c.email))
		}
		emails[c.email] = true
		_, domain, _ := strings.Cut(c.email, "@")
		if core.ReservedEmailDomain(domain) {
			t.Errorf("customer email at a reserved domain: %s", core.MaskEmail(c.email))
		}
		if core.FictionalPhone(c.phone) {
			t.Errorf("customer phone in a fictional range: %s", core.MaskPhone(c.phone))
		}
	}
	long := 0
	for _, row := range s.asActions().rows {
		if row[len(row)-1] != nil {
			long++
			if a, _ := row[6].(string); len(a) != 32 {
				t.Errorf("long action args %q, want its md5", a)
			}
			if e, _ := row[len(row)-1].(string); len(e) <= asArgsLimit {
				t.Errorf("extended_args is %d bytes, want over %d", len(e), asArgsLimit)
			}
		}
	}
	if long != 1 || len(s.asActions().rows) != actionCount {
		t.Errorf("%d actions with %d long, want %d with 1", len(s.asActions().rows), long, actionCount)
	}
	dump := string(shopBytes(t, Options{Seed: 5}))
	for _, want := range []string{
		"'siteurl','" + productionURL + "'",
		`https:\\/\\/shop.example.com\\/wp-content`, // JSON-escaped, then SQL-escaped
		`s:30:\"https://shop.example.com/sale/\";`,  // serialized
		`s:8:\"testmode\";s:2:\"no\";`,
		"'active','Orders to the CRM'",
		"Customer rang from +",
		"'order_note'",
	} {
		if !strings.Contains(dump, want) {
			t.Errorf("dump lacks %q", want)
		}
	}
	live := testutil.J("sk", "_live_")
	if !strings.Contains(dump, live) || !strings.HasPrefix(s.stripeKey, live) {
		t.Error("dump lacks the live-looking Stripe key")
	}
	if got := Production(); len(got) != 1 || got[0] != "shop.example.com" {
		t.Errorf("Production() = %v", got)
	}
}

// Every serialized value in the dump is one PHP itself unserializes.
func TestShopSerializedIsValidPHP(t *testing.T) {
	php := testutil.RequirePHP(t)
	s := newShop(Options{Seed: 2})
	var values []string
	for _, tb := range []*table{s.options(), s.usermeta(), s.postmeta(), s.asActions()} {
		for _, row := range tb.rows {
			for _, cell := range row {
				if v, ok := cell.(string); ok && (strings.HasPrefix(v, "a:") || strings.HasPrefix(v, "O:")) {
					values = append(values, v)
				}
			}
		}
	}
	if len(values) < 2*DefaultCustomers {
		t.Fatalf("only %d serialized values", len(values))
	}
	// Each value goes in length-prefixed so NUL bytes survive.
	var in bytes.Buffer
	for _, v := range values {
		fmt.Fprintf(&in, "%08d", len(v))
		in.WriteString(v)
	}
	script := `$in = stream_get_contents(STDIN); $bad = 0; $n = 0;
for ($i = 0; $i < strlen($in);) { $l = (int) substr($in, $i, 8); $v = substr($in, $i + 8, $l); $i += 8 + $l; $n++;
  if ($v !== 'b:0;' && @unserialize($v, ['allowed_classes' => false]) === false) { $bad++; } }
echo "$n $bad";`
	cmd := exec.Command(php, "-r", script)
	cmd.Stdin = &in
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(out), strconv.Itoa(len(values))+" 0"; got != want {
		t.Errorf("php checked and failed: %s, want %s", got, want)
	}
}

func TestWPConfig(t *testing.T) {
	a, b := WPConfig(Options{Seed: 1}), WPConfig(Options{Seed: 1})
	if !bytes.Equal(a, b) {
		t.Fatal("two runs differ")
	}
	if bytes.Equal(a, WPConfig(Options{Seed: 2})) {
		t.Error("another seed gave the same salts")
	}
	if bytes.Contains(a, []byte("WP_ENVIRONMENT_TYPE")) {
		t.Error("wp-config defines WP_ENVIRONMENT_TYPE")
	}
	if !bytes.Contains(a, []byte("require_once ABSPATH . 'wp-settings.php';")) {
		t.Error("wp-config lacks the wp-settings.php line")
	}
	php := testutil.RequirePHP(t)
	path := filepath.Join(t.TempDir(), "wp-config.php")
	if err := os.WriteFile(path, a, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(php, "-l", path).CombinedOutput(); err != nil {
		t.Errorf("php -l: %v\n%s", err, out)
	}
}

// No live- or test-format Stripe key is written literally in the package:
// push protection blocks them and they would teach a scanner the wrong thing.
func TestNoLiteralKeys(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("glob: %v %v", files, err)
	}
	needles := []string{testutil.J("sk", "_live_"), testutil.J("sk", "_test_"), testutil.J("pk", "_live_"), testutil.J("rk", "_live_")}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range needles {
			if bytes.Contains(b, []byte(n)) {
				t.Errorf("%s holds a literal %s key", f, n)
			}
		}
	}
}
