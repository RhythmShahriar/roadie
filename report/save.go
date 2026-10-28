// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/RhythmShahriar/rhythm/kit/cache"
	"github.com/RhythmShahriar/rhythm/kit/config"

	"github.com/RhythmShahriar/roadie/core"
)

// stampLayout names a report folder by its run's start, in UTC: the Z
// says so.
const stampLayout = "2006-01-02T15-04-05Z"

// now is the clock Save falls back to when a report has no start time;
// tests replace it.
var now = time.Now

// Save writes report.json and report.html under dir's
// .roadie/reports/<ts>-<kind>-<target>/ and returns the folder.
//
// <ts> is the run's start in UTC (2006-01-02T15-04-05Z), <kind> copy,
// prepare, verify or plan and <target> the last part of the target's
// name (staging.sql, staging), with -2, -3 ... added when two such runs
// start in the same second. The folders are 0700 and
// the files 0600, written atomically through kit's cache.WriteFile:
// reports describe production. When dir's folder can't be written, the
// report goes to <config.CacheDir("roadie")>/reports/<ts>/ instead.
func Save(dir string, e *core.Envelope, html []byte) (string, error) {
	if e == nil {
		return "", errors.New("report: no envelope")
	}
	js, err := encodeEnvelope(e)
	if err != nil {
		return "", err
	}
	stamp := now().UTC()
	if d, err := ParseData(e); err == nil {
		if t, err := time.Parse(time.RFC3339, d.Started); err == nil {
			stamp = t.UTC()
		}
	}
	name := stamp.Format(stampLayout)
	if e.Kind != "" {
		name += "-" + slug(e.Kind)
	}
	if d, err := ParseData(e); err == nil {
		if t := slug(targetName(d.Target.Display)); t != "" {
			name += "-" + t
		}
	}
	folder, err := saveIn(filepath.Join(dir, ".roadie", "reports"), name, js, html)
	if err == nil {
		return folder, nil
	}
	cacheDir, cerr := config.CacheDir("roadie")
	if cerr != nil {
		return "", err
	}
	folder, ferr := saveIn(filepath.Join(cacheDir, "reports"), name, js, html)
	if ferr != nil {
		return "", errors.Join(err, ferr)
	}
	return folder, nil
}

// targetName is the last part of a target's name: the file of a path,
// the database of a DSN, the folder of an ssh target. A query or
// credentials never reach it.
func targetName(display string) string {
	if i := strings.IndexAny(display, "?#"); i >= 0 {
		display = display[:i]
	}
	if _, rest, ok := strings.Cut(display, "://"); ok {
		display = rest
		if at := strings.LastIndex(display, "@"); at >= 0 {
			display = display[at+1:]
		}
	}
	display = strings.TrimRight(display, `/\`)
	if i := strings.LastIndexAny(display, `/\`); i >= 0 {
		display = display[i+1:]
	}
	return display
}

// slug keeps letters, digits, dots, underscores and dashes of s, turns
// anything else into a dash and cuts it to 40 bytes, so it is safe in a
// folder name on every OS.
func slug(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s) && len(b) < 40; i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-':
			b = append(b, c)
		default:
			b = append(b, '-')
		}
	}
	return strings.Trim(string(b), ".-")
}

// saveIn makes a new folder under root and writes both files into it.
func saveIn(root, name string, js, html []byte) (string, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	folder := ""
	for i := 1; i < 1000; i++ {
		f := filepath.Join(root, name)
		if i > 1 {
			f += "-" + strconv.Itoa(i)
		}
		err := os.Mkdir(f, 0o700)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		folder = f
		break
	}
	if folder == "" {
		return "", fmt.Errorf("no free report folder for %s in %s", name, root)
	}
	if err := cache.WriteFile(filepath.Join(folder, "report.json"), js); err != nil {
		return "", err
	}
	if err := cache.WriteFile(filepath.Join(folder, "report.html"), html); err != nil {
		return "", err
	}
	return folder, nil
}

// marshalIndent writes v indented two spaces with no HTML escaping and a
// newline at the end, as every JSON file Roadie writes.
func marshalIndent(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}
