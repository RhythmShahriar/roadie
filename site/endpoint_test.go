// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RhythmShahriar/rhythm/kit/cli"

	"github.com/RhythmShahriar/roadie/core"
)

func TestParseEndpointShapes(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "export.bin")
	if err := os.WriteFile(file, []byte("-- dump"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		in   string
		want core.Endpoint
	}{
		{"-", core.Endpoint{Kind: core.Stdio, Display: "-"}},
		{"shop.sql", core.Endpoint{Kind: core.DumpFile, Display: "shop.sql", Path: "shop.sql"}},
		{"shop.sql.gz", core.Endpoint{Kind: core.DumpFile, Display: "shop.sql.gz", Path: "shop.sql.gz", Compression: "gzip"}},
		{"out/SHOP.SQL.ZST", core.Endpoint{Kind: core.DumpFile, Display: "out/SHOP.SQL.ZST", Path: "out/SHOP.SQL.ZST", Compression: "zstd"}},
		{"shop.sql.bz2", core.Endpoint{Kind: core.DumpFile, Display: "shop.sql.bz2", Path: "shop.sql.bz2", Compression: "bzip2"}},
		{"prod.dump", core.Endpoint{Kind: core.DumpFile, Display: "prod.dump", Path: "prod.dump"}},
		{file, core.Endpoint{Kind: core.DumpFile, Display: file, Path: file}},
		{dir, core.Endpoint{Kind: core.LocalSite, Display: dir, Path: dir}},
		{"@prod", core.Endpoint{Kind: core.WPAlias, Display: "@prod", Alias: "@prod"}},
		{"ssh://deploy@shop.example/var/www/shop", core.Endpoint{Kind: core.SSHSite, Display: "ssh://deploy@shop.example/var/www/shop", User: "deploy", Host: "shop.example", Path: "/var/www/shop"}},
		{"ssh://host.example:2222/srv/site", core.Endpoint{Kind: core.SSHSite, Display: "ssh://host.example:2222/srv/site", Host: "host.example", Port: 2222, Path: "/srv/site"}},
		{"ssh://deploy@[2001:db8::1]:22/srv", core.Endpoint{Kind: core.SSHSite, Display: "ssh://deploy@[2001:db8::1]:22/srv", User: "deploy", Host: "[2001:db8::1]", Port: 22, Path: "/srv"}},
		{"deploy@shop.example:/var/www/shop", core.Endpoint{Kind: core.SSHSite, Display: "deploy@shop.example:/var/www/shop", User: "deploy", Host: "shop.example", Path: "/var/www/shop"}},
		{"deploy@[2001:db8::1]:/srv", core.Endpoint{Kind: core.SSHSite, Display: "deploy@[2001:db8::1]:/srv", User: "deploy", Host: "[2001:db8::1]", Path: "/srv"}},
		{"mysql://shop@db.example:3307/shop", core.Endpoint{Kind: core.DSN, Display: "mysql://shop@db.example:3307/shop", User: "shop", Host: "db.example", Port: 3307, Path: "shop", Driver: core.MySQL, DSN: "mysql://shop@db.example:3307/shop"}},
		{"mariadb://db.example/shop", core.Endpoint{Kind: core.DSN, Display: "mariadb://db.example/shop", Host: "db.example", Path: "shop", Driver: core.MariaDB, DSN: "mariadb://db.example/shop"}},
		{"postgres://app@pg.example/app?sslmode=require", core.Endpoint{Kind: core.DSN, Display: "postgres://app@pg.example/app?sslmode=require", User: "app", Host: "pg.example", Path: "app", Driver: core.Postgres, DSN: "postgres://app@pg.example/app?sslmode=require"}},
		{"postgresql://app@pg.example/app", core.Endpoint{Kind: core.DSN, Display: "postgresql://app@pg.example/app", User: "app", Host: "pg.example", Path: "app", Driver: core.Postgres, DSN: "postgresql://app@pg.example/app"}},
		{"sqlite:///srv/app/db.sqlite", core.Endpoint{Kind: core.DSN, Display: "sqlite:///srv/app/db.sqlite", Path: filepath.FromSlash("/srv/app/db.sqlite"), Driver: core.SQLite, DSN: "sqlite:///srv/app/db.sqlite"}},
		{"sqlite:///C:/app/db.sqlite", core.Endpoint{Kind: core.DSN, Display: "sqlite:///C:/app/db.sqlite", Path: filepath.FromSlash("C:/app/db.sqlite"), Driver: core.SQLite, DSN: "sqlite:///C:/app/db.sqlite"}},
		// Windows paths are local, never scp-like ssh.
		{`C:\backups\shop.sql`, core.Endpoint{Kind: core.DumpFile, Display: `C:\backups\shop.sql`, Path: `C:\backups\shop.sql`}},
		{`C:/backups/shop.sql.gz`, core.Endpoint{Kind: core.DumpFile, Display: `C:/backups/shop.sql.gz`, Path: `C:/backups/shop.sql.gz`, Compression: "gzip"}},
		{`\\server\share\shop.sql`, core.Endpoint{Kind: core.DumpFile, Display: `\\server\share\shop.sql`, Path: `\\server\share\shop.sql`}},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseEndpoint(tt.in)
			if err != nil {
				t.Fatalf("ParseEndpoint(%q): %v", tt.in, err)
			}
			tt.want.Raw = tt.in
			if got != tt.want {
				t.Errorf("ParseEndpoint(%q)\n got %+v\nwant %+v", tt.in, fields(got), fields(tt.want))
			}
		})
	}
}

// fields prints every field, since Endpoint's String shows only Display.
func fields(e core.Endpoint) string {
	return fmt.Sprintf("{Kind:%s Display:%s Path:%s User:%s Host:%s Port:%d Alias:%s Driver:%s DSN:%s Compression:%s pw:%t}",
		e.Kind, e.Display, e.Path, e.User, e.Host, e.Port, e.Alias, e.Driver, e.DSN, e.Compression, e.Password != "")
}

func TestParseEndpointPassword(t *testing.T) {
	const pw = "s3cret-Canary"
	tests := []struct{ in, display, dsn string }{
		{"mysql://shop:" + pw + "@db.example/shop", "mysql://shop:•••@db.example/shop", "mysql://shop@db.example/shop"},
		{"postgres://app:" + url.QueryEscape(pw+"/@:") + "@pg.example:5433/app", "postgres://app:•••@pg.example:5433/app", "postgres://app@pg.example:5433/app"},
		{"postgres://app@pg.example/app?password=" + pw + "&sslmode=disable", "postgres://app:•••@pg.example/app?sslmode=disable", "postgres://app@pg.example/app?sslmode=disable"},
	}
	for _, tt := range tests {
		e, err := ParseEndpoint(tt.in)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(e.Password, pw) {
			t.Errorf("Password of %s not moved out of the DSN", tt.display)
		}
		if e.Display != tt.display || e.DSN != tt.dsn {
			t.Errorf("Display %q DSN %q, want %q %q", e.Display, e.DSN, tt.display, tt.dsn)
		}
		for _, s := range []string{e.Display, e.DSN, e.String(), fmt.Sprintf("%v %#v", e, e)} {
			if strings.Contains(s, pw) {
				t.Errorf("%q shows the password", s)
			}
		}
		if !InlinePassword(e) {
			t.Errorf("InlinePassword(%s) = false", e.Display)
		}
	}
	e, err := ParseEndpoint("mysql://shop@db.example/shop")
	if err != nil {
		t.Fatal(err)
	}
	if InlinePassword(e) {
		t.Error("InlinePassword true for a DSN without a password")
	}
	if got, want := InlinePasswordWarning("from"), "the password in --from is visible to other users of this machine; use ROADIE_FROM_PASSWORD"; got != want {
		t.Errorf("warning %q, want %q", got, want)
	}
	if got := InlinePasswordWarning("to"); !strings.HasSuffix(got, "use ROADIE_TO_PASSWORD") {
		t.Errorf("warning %q", got)
	}
}

func TestParseEndpointRefused(t *testing.T) {
	tests := []struct{ in, msg string }{
		{"", "endpoint: give a dump file"},
		{"nothing-here", "endpoint: nothing-here is not a dump file, site folder, ssh://, @alias or DSN"},
		{"https://shop.example/", "endpoint: https://shop.example/ is not a dump file, site folder, ssh://, @alias or DSN"},
		{"ftp://u:hunter2-pw@h.example/x", "endpoint: ftp://u:•••@h.example/x is not a dump file"},
		{"ssh://-oProxyCommand=touch /tmp/x/srv", "refused"},
		{"ssh://-oProxyCommand=x/srv", `ssh host "-oProxyCommand=x" is refused: a host can't start with '-'`},
		{"-oProxyCommand=touch /tmp/x:/srv", "is not a dump file"},
		{"deploy@-oProxyCommand=touch /tmp/x:/srv", `ssh host "-oProxyCommand=touch /tmp/x" is refused`},
		{"deploy@a b:/srv", `ssh host "a b" is refused: a host can't hold whitespace or control characters`},
		{"deploy@a;id:/srv", `ssh host "a;id" is refused: a host can't hold ";"`},
		{"de$(id)@host.example:/srv", `ssh user "de$(id)" is refused`},
		{"ssh://u:pw@host.example/srv", "holds a password"},
		{"ssh://host.example", "needs the site's folder"},
		{"ssh://host.example:0/srv", "bad port"},
		{"ssh://host.example/srv?x=1", "takes no ?query"},
		{"deploy@host.example:/srv/a\nb", "control character"},
		{"deploy@host.example:~/www", "endpoint: deploy@host.example:~/www: give the absolute folder on the host, such as /home/deploy/www; Roadie doesn't expand '~'"},
		{"deploy@host.example:~deploy/www", "Roadie doesn't expand '~'"},
		{"ssh://deploy@host.example/~/www", "endpoint: ssh://deploy@host.example/~/www: give the absolute folder on the host"},
		{"@", "needs an alias name"},
		{"@pro d", "is not a WP-CLI alias name"},
		{"sqlite://relative.db", "sqlite:///absolute/path.sqlite"},
		{"mysql:///shop", "has no host"},
		{"mysql://db.example:99999/shop", "bad port"},
	}
	for _, tt := range tests {
		_, err := ParseEndpoint(tt.in)
		var ue cli.UsageError
		if !errors.As(err, &ue) {
			t.Errorf("ParseEndpoint(%q) = %v, want a usage error (exit 2)", tt.in, err)
			continue
		}
		if !strings.Contains(err.Error(), tt.msg) {
			t.Errorf("ParseEndpoint(%q) = %q, want it to contain %q", tt.in, err, tt.msg)
		}
		if strings.Contains(err.Error(), "hunter2-pw") {
			t.Errorf("error shows the password: %q", err)
		}
	}
}

// A path that can't be stat'ed for a reason other than "not there" says why.
func TestParseEndpointStatError(t *testing.T) {
	old := statFunc
	t.Cleanup(func() { statFunc = old })
	statFunc = func(string) (os.FileInfo, error) {
		return nil, &os.PathError{Op: "stat", Path: "x", Err: os.ErrPermission}
	}
	_, err := ParseEndpoint("locked")
	if err == nil || !strings.Contains(err.Error(), "endpoint: locked can't be read: permission denied") {
		t.Fatalf("got %v", err)
	}
}

func FuzzEndpoint(f *testing.F) {
	for _, s := range []string{
		"-", "@prod", "shop.sql.gz", "ssh://deploy@shop.example:2222/var/www", "deploy@shop.example:/srv",
		"mysql://u:p@h/db", "postgres://u@h/db?password=p", "sqlite:///a.db", `C:\x`, `\\s\x`, "a@[::1]:/x",
		"ssh://-oProxyCommand=x/a", "mysql://%zz", "ftp://u:p@h",
	} {
		f.Add(s, "pw-Canary-1")
	}
	f.Fuzz(func(t *testing.T, s, pw string) {
		e, err := ParseEndpoint(s)
		if err == nil && e.String() != e.Display {
			t.Fatalf("String %q != Display %q", e.String(), e.Display)
		}
		if err == nil && e.Kind == "" {
			t.Fatalf("no kind for %q", s)
		}
		// The same DSN with two passwords must print the same way, so the
		// password can never reach Display or DSN.
		if pw == "" {
			return
		}
		pw = "Pw" + pw
		raw := func(p string) string {
			return "mysql://" + url.UserPassword("user", p).String() + "@db.example/" + url.PathEscape(s) + "?x=" + url.QueryEscape(s)
		}
		a, errA := ParseEndpoint(raw(pw))
		b, errB := ParseEndpoint(raw("other"))
		if (errA == nil) != (errB == nil) {
			t.Fatalf("password changes whether %q parses: %v / %v", s, errA, errB)
		}
		if errA != nil {
			if strings.Contains(errA.Error(), pw) && !strings.Contains(raw("other"), pw) {
				t.Fatalf("error shows the password: %v", errA)
			}
			return
		}
		if a.Display != b.Display || a.DSN != b.DSN {
			t.Fatalf("Display or DSN depends on the password: %q %q / %q %q", a.Display, a.DSN, b.Display, b.DSN)
		}
		if a.Password != pw {
			t.Fatalf("Password %q, want %q", a.Password, pw)
		}
	})
}
