// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wpconfig

import (
	"io/fs"
	"strconv"
	"strings"
	"testing"
)

// files is a site's files for Resolve.
func files(m map[string]string) Reader {
	return func(rel string) ([]byte, error) {
		if b, ok := m[rel]; ok {
			return []byte(b), nil
		}
		return nil, fs.ErrNotExist
	}
}

const stagingTail = "define( 'WP_ENVIRONMENT_TYPE', 'staging' );\ndefine( 'DISABLE_WP_CRON', true );\n/* That's all, stop editing! */\nrequire_once ABSPATH . 'wp-settings.php';\n"

func TestResolveIncludeFirstWins(t *testing.T) {
	for _, head := range []string{
		"require __DIR__ . '/wp-config-local.php';",
		"require_once( dirname( __FILE__ ) . '/wp-config-local.php' );",
		"include 'wp-config-local.php';",
		"include_once ABSPATH . 'wp-config-local.php';",
	} {
		f := mustParse(t, []byte("<?php\n"+head+"\n"+stagingTail))
		read := files(map[string]string{"wp-config-local.php": "<?php\ndefine('WP_ENVIRONMENT_TYPE','production');\ndefine('DISABLE_WP_CRON', false);\n"})
		r := f.Resolve("WP_ENVIRONMENT_TYPE", read)
		if r.Unknown != "" || !r.Found || r.Value != "production" || r.File != "wp-config-local.php" || r.Line != 2 {
			t.Errorf("%s: got %+v, want production from wp-config-local.php line 2", head, r)
		}
		if r := f.Resolve("DISABLE_WP_CRON", read); r.Value != "false" || r.File != "wp-config-local.php" {
			t.Errorf("%s: DISABLE_WP_CRON %+v, want false from the included file", head, r)
		}
		// Not there: the include defines nothing.
		if r := f.Resolve("WP_ENVIRONMENT_TYPE", files(nil)); r.Unknown != "" || r.Value != "staging" || r.File != "wp-config.php" {
			t.Errorf("%s: missing include: got %+v, want wp-config.php's staging", head, r)
		}
	}
}

func TestResolveUnknownInclude(t *testing.T) {
	for _, head := range []string{
		"require getenv('CONFIG');",
		"require '/etc/wordpress/config.php';",
		"require __DIR__ . '/../shared.php';",
	} {
		f := mustParse(t, []byte("<?php\n"+head+"\n"+stagingTail))
		r := f.Resolve("WP_ENVIRONMENT_TYPE", files(nil))
		if r.Unknown == "" || r.UnknownLine != 2 || r.Value != "staging" {
			t.Errorf("%s: got %+v, want unknown at line 2", head, r)
		}
	}
	// A conditional include of a file that defines it may win.
	f := mustParse(t, []byte("<?php\nif ( file_exists( __DIR__ . '/local.php' ) ) { include __DIR__ . '/local.php'; }\n"+stagingTail))
	r := f.Resolve("WP_ENVIRONMENT_TYPE", files(map[string]string{"local.php": "<?php define('WP_ENVIRONMENT_TYPE','production');"}))
	if r.Unknown == "" || !strings.Contains(r.Unknown, "local.php") {
		t.Errorf("conditional include: got %+v, want unknown", r)
	}
	// An include after the define can't change it.
	f = mustParse(t, []byte("<?php\n"+strings.Replace(stagingTail, "/* That's", "require 'x.php';\n/* That's", 1)))
	if r := f.Resolve("WP_ENVIRONMENT_TYPE", files(nil)); r.Unknown != "" || r.Value != "staging" {
		t.Errorf("include after the define: got %+v", r)
	}
	if got := len(f.Includes()); got != 1 {
		t.Errorf("Includes() = %d, want 1 (wp-settings.php is not one)", got)
	}
}

func TestConstDefinesFirst(t *testing.T) {
	f := mustParse(t, []byte("<?php\nconst WP_ENVIRONMENT_TYPE = \"production\", OTHER = 1;\n"+stagingTail))
	d, ok := f.Lookup("WP_ENVIRONMENT_TYPE")
	if !ok || d.Value != "production" || !d.Const || d.Line != 2 {
		t.Fatalf("Lookup = %+v, want the const's production", d)
	}
	if d, ok := f.Lookup("OTHER"); !ok || d.Value != "1" || !d.Literal {
		t.Errorf("OTHER = %+v", d)
	}
	// Set changes the const, which is the one PHP keeps.
	if err := f.Set("WP_ENVIRONMENT_TYPE", "staging"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(f.Bytes()), `const WP_ENVIRONMENT_TYPE = "staging", OTHER = 1;`) {
		t.Errorf("Set didn't edit the const:\n%s", f.Bytes())
	}
	// A class constant or a namespaced one is not the global constant.
	for _, src := range []string{
		"<?php\nclass A { const WP_ENVIRONMENT_TYPE = 'production'; }\n" + stagingTail,
		"<?php\nnamespace Site;\nconst WP_ENVIRONMENT_TYPE = 'production';\n" + stagingTail,
	} {
		if d, _ := mustParse(t, []byte(src)).Lookup("WP_ENVIRONMENT_TYPE"); d.Value != "staging" {
			t.Errorf("got %q, want staging for\n%s", d.Value, src)
		}
	}
}

func TestStrings(t *testing.T) {
	src := "<?php return array (\n  'app' => array ( 'env' => 'production', ),\n  'services' => [\n    'stripe' => [ 'secret' => 'sk-value', 'list' => ['a'] ],\n  ],\n  3 => 'three',\n);\n"
	pairs, err := Strings([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"app.env=production@2", "services.stripe.secret=sk-value@4", "services.stripe.list=a@4", "3=three@6"}
	var got []string
	for _, p := range pairs {
		got = append(got, p.Name+"="+p.Value+"@"+strconv.Itoa(p.Line))
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("got %v\nwant %v", got, want)
	}
}
