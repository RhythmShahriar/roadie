// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package wpconfig

import (
	"errors"
	"io/fs"
	"path"
	"strconv"
	"strings"
)

// Include is one require or include of a file other than wp-settings.php
// that runs before wp-settings.php.
type Include struct {
	Line int
	// Path is the file relative to the folder of the file holding the
	// statement, "" when it is an expression Roadie can't follow.
	Path        string
	Expr        string // the statement, on one line
	Conditional bool
}

// Includes returns every require or include before wp-settings.php, other
// than wp-settings.php itself, in file order.
func (f *File) Includes() []Include {
	var out []Include
	for _, in := range f.p.includes {
		if f.p.settingsAt >= 0 && in.start > f.p.settingsAt {
			continue
		}
		out = append(out, Include{Line: in.line, Path: in.path, Expr: in.expr, Conditional: in.conditional})
	}
	return out
}

// Resolved is the define PHP keeps for a name once the files wp-config.php
// includes before it are read.
type Resolved struct {
	Define
	Found bool
	// File is where the define is: "wp-config.php" or an included file,
	// relative to the site.
	File string
	// Unknown says why Roadie can't tell which define PHP keeps: a file
	// included first that it can't follow or read, or one that may define
	// the name first. Define and File are then wp-config.php's own.
	Unknown string
	// UnknownLine is the wp-config.php line of the include behind Unknown.
	UnknownLine int
}

// Reader reads a file of the site by its path relative to the site.
type Reader func(rel string) ([]byte, error)

// maxIncludeDepth bounds how deep Resolve follows includes.
const maxIncludeDepth = 4

// Resolve returns the define PHP keeps for name, following every file
// included before it (PHP keeps the first define, so one in a file
// required at the top of wp-config.php wins over wp-config.php's own).
// read reads the included files; one that isn't there defines nothing.
func (f *File) Resolve(name string, read Reader) Resolved {
	own := Resolved{File: "wp-config.php"}
	if d := f.p.effective(name); d != nil {
		own.Define, own.Found = d.Define, true
	}
	r, ok := resolveIn(f.p, "wp-config.php", name, read, 0)
	switch {
	case !ok:
		return own
	case r.Unknown != "":
		own.Unknown, own.UnknownLine = r.Unknown, r.UnknownLine
		return own
	}
	return r
}

// resolveIn resolves name in the file p parsed, which is file. ok is false
// when no file it includes first defines name (or may define it); the
// caller then keeps its own define.
func resolveIn(p *parsed, file, name string, read Reader, depth int) (Resolved, bool) {
	stop := -1
	if d := p.effective(name); d != nil && !d.Conditional {
		stop = d.callStart
	}
	unknown := func(in incl, why string) (Resolved, bool) {
		return Resolved{Unknown: why, UnknownLine: in.line}, true
	}
	for _, in := range p.includes {
		if p.settingsAt >= 0 && in.start > p.settingsAt || stop >= 0 && in.start > stop {
			break
		}
		at := file + " line " + strconv.Itoa(in.line)
		if in.path == "" {
			return unknown(in, at+" runs "+in.expr+" first, a file Roadie can't follow, which may define "+name)
		}
		rel := in.rel(file)
		if rel == ".." || strings.HasPrefix(rel, "../") {
			return unknown(in, at+" includes "+in.path+" first, outside the site, which may define "+name)
		}
		if depth >= maxIncludeDepth {
			return unknown(in, at+" includes "+rel+" first, deeper than Roadie follows, which may define "+name)
		}
		b, err := read(rel)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return unknown(in, at+" includes "+rel+" first, which can't be read, so it may define "+name)
		}
		sub, err := parse(b)
		if err != nil {
			return unknown(in, at+" includes "+rel+" first, which Roadie can't parse, so it may define "+name)
		}
		r, ok := resolveIn(sub, rel, name, read, depth+1)
		if !ok {
			if d := sub.effective(name); d != nil {
				r, ok = Resolved{Define: d.Define, Found: true, File: rel}, true
			}
		}
		if !ok {
			continue
		}
		if r.Unknown != "" {
			r.UnknownLine = in.line
			return r, true
		}
		if in.conditional || r.Conditional || earlierDefine(p, name, in.start) {
			return unknown(in, at+" may include "+rel+" first, which defines "+name)
		}
		// The included file's define comes before anything in this one.
		r.BeforeSettings = true
		return r, true
	}
	return Resolved{}, false
}

// rel is the file an include in file names, relative to the site: ABSPATH
// is the site's folder, __DIR__ the folder of file.
func (in incl) rel(file string) string {
	if in.root {
		return in.path
	}
	return path.Join(path.Dir(file), in.path)
}

// earlierDefine reports a define of name before offset at.
func earlierDefine(p *parsed, name string, at int) bool {
	for _, d := range p.defs {
		if d.Name == name && d.callStart < at {
			return true
		}
	}
	return false
}

// Included is a file wp-config.php includes, parsed.
type Included struct {
	// Rel is the file relative to the site.
	Rel  string
	File *File
	// Via is the statement that includes it: "wp-config.php line 2".
	Via string
}

// IncludedFiles returns every file wp-config.php includes and the files
// those include, in the order PHP reaches them, each once. A file that
// isn't there is left out: it defines nothing. unknown says, for each
// include Roadie can't follow (an expression, a file outside the site,
// one too deep, unreadable or not parsed), where it is and why, so a
// caller that reads the files for keys can say what it didn't read.
func (f *File) IncludedFiles(read Reader) (files []Included, unknown []string) {
	seen := map[string]bool{"wp-config.php": true}
	var walk func(p *parsed, file string, depth int)
	walk = func(p *parsed, file string, depth int) {
		for _, in := range p.includes {
			at := file + " line " + strconv.Itoa(in.line)
			if in.path == "" {
				unknown = append(unknown, at+" runs "+in.expr+", a file Roadie can't follow")
				continue
			}
			rel := in.rel(file)
			switch {
			case rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel):
				unknown = append(unknown, at+" includes "+in.path+", outside the site")
				continue
			case seen[rel]:
				continue
			case depth >= maxIncludeDepth:
				unknown = append(unknown, at+" includes "+rel+", deeper than Roadie follows")
				continue
			}
			seen[rel] = true
			b, err := read(rel)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				unknown = append(unknown, at+" includes "+rel+", which can't be read")
				continue
			}
			sub, err := Parse(b)
			if err != nil {
				unknown = append(unknown, at+" includes "+rel+", which Roadie can't parse")
				continue
			}
			files = append(files, Included{Rel: rel, File: sub, Via: at})
			walk(sub.p, rel, depth+1)
		}
	}
	walk(f.p, "wp-config.php", 0)
	return files, unknown
}
