// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package keys

import (
	"bytes"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

// secretsPath is the kit package whose Redact must never touch data.
const secretsPath = "github.com/RhythmShahriar/rhythm/kit/secrets"

// redactAllowed lists the display packages that may call secrets.Redact.
// None in v1: masking for display goes through core.Mask*.
var redactAllowed = []string{}

// redactCalls returns each place in src that uses secrets.Redact, as
// "file:line".
func redactCalls(fset *token.FileSet, name string, src []byte) ([]string, error) {
	f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	local := ""
	for _, im := range f.Imports {
		p, _ := strconv.Unquote(im.Path.Value)
		if p != secretsPath {
			continue
		}
		local = "secrets"
		if im.Name != nil {
			local = im.Name.Name
		}
	}
	if local == "" || local == "_" {
		return nil, nil
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if id, ok := x.X.(*ast.Ident); ok && id.Name == local && x.Sel.Name == "Redact" {
				out = append(out, fset.Position(x.Pos()).String())
			}
		case *ast.Ident:
			if local == "." && x.Name == "Redact" {
				out = append(out, fset.Position(x.Pos()).String())
			}
		}
		return true
	})
	return out, nil
}

// secrets.Redact leaves serialized lengths wrong, so it is display-only
// (PRD 12.2 and 18.5). This walks every package of the module that go list
// reports and fails on any use outside redactAllowed.
func TestRedactNeverUsedOnData(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		goBin = filepath.Join(os.Getenv("GOROOT"), "bin", "go")
		if _, err := os.Stat(goBin); err != nil {
			t.Skip("go binary not on PATH")
		}
	}
	cmd := exec.Command(goBin, "list", "-deps", "-json", "./...")
	cmd.Dir = ".."
	cmd.Env = append(os.Environ(), "GOWORK=off")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, stderr.String())
	}
	type pkg struct {
		ImportPath string
		Dir        string
		GoFiles    []string
		Module     *struct{ Path string }
	}
	fset := token.NewFileSet()
	checked := 0
	for d := json.NewDecoder(bytes.NewReader(out)); ; {
		var p pkg
		if err := d.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if p.Module == nil || p.Module.Path != "github.com/RhythmShahriar/roadie" {
			continue
		}
		checked++
		for _, f := range p.GoFiles {
			path := filepath.Join(p.Dir, f)
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			uses, err := redactCalls(fset, path, src)
			if err != nil {
				t.Fatal(err)
			}
			if len(uses) > 0 && !slices.Contains(redactAllowed, p.ImportPath) {
				t.Errorf("%s uses secrets.Redact at %v; it is for display only, data goes through keys.Replace", p.ImportPath, uses)
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d module packages listed; the walk is broken", checked)
	}
}

// The checker itself finds a plain, a renamed and a dot import.
func TestRedactCallsFindsUses(t *testing.T) {
	cases := map[string]int{
		`package x; import "` + secretsPath + `"; var _ = secrets.Redact("a")`:            1,
		`package x; import s "` + secretsPath + `"; var _ = s.Redact("a")`:                1,
		`package x; import . "` + secretsPath + `"; var _ = Redact("a")`:                  1,
		`package x; import "` + secretsPath + `"; var _ = secrets.Mask("a", 4)`:           0,
		`package x; import "strings"; var _ = strings.Redact`:                             0,
		`package x; import "` + secretsPath + `"; var _ = secrets.RedactHeader("a", "b")`: 0,
	}
	for src, want := range cases {
		got, err := redactCalls(token.NewFileSet(), "x.go", []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != want {
			t.Errorf("%s: %d uses, want %d", src, len(got), want)
		}
	}
}
