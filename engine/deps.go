// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/RhythmShahriar/roadie/codec/nested"
	"github.com/RhythmShahriar/roadie/codec/urlrw"
	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/detect"
	"github.com/RhythmShahriar/roadie/dump"
	"github.com/RhythmShahriar/roadie/keys"
	"github.com/RhythmShahriar/roadie/pseudo"
)

// The other lanes' functions the engine calls, as package variables so a
// unit test can swap in a fake (each test that does restores them and
// doesn't run in parallel). Production code never assigns them.
var (
	textSpans     = detect.TextSpans
	newFaker      = pseudo.NewFaker
	newKnown      = pseudo.NewKnown
	scrubText     = pseudo.Scrub
	resetHash     = pseudo.ResetHash
	passwordHash  = pseudo.PasswordHash
	nestedRewrite = nested.Rewrite
	nestedLeaves  = nested.Leaves
	nestedFormat  = nested.Format
	scanKeys      = keys.ScanValue
	replaceKey    = keys.Replace
	openDump      = dump.Open
	lookupEnv     = os.LookupEnv
	newURLMap     = func(pairs []urlrw.Pair) (urlRewriter, error) {
		m, err := urlrw.NewMap(pairs)
		if err != nil {
			return nil, err
		}
		return m, nil
	}
	newSpool = func(dir string) (spool, error) {
		s, err := dump.NewSpool(dir)
		if err != nil {
			return nil, err
		}
		return s, nil
	}
)

// urlRewriter is what the engine needs of a *urlrw.Map.
type urlRewriter interface {
	Rewrite(text []byte) ([]byte, int)
	RewriteHost(host []byte) ([]byte, bool)
}

// spool is what the engine needs of a *dump.Spool: the encrypted copy of
// a source that can't be read twice.
type spool interface {
	Writer() io.WriteCloser
	Reader() (io.ReadCloser, error)
	Remove() error
}

// openDumpFile opens a dump file for one pass; closing the source closes
// the file.
func openDumpFile(ctx context.Context, path string, o dump.Options) (core.Source, error) {
	f, err := os.Open(path) // #nosec G304 -- the dump the user named
	if err != nil {
		return nil, err
	}
	src, err := openDump(ctx, f, o)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &closingSource{Source: src, c: f}, nil
}

// closingSource closes an extra reader after its source.
type closingSource struct {
	core.Source
	c io.Closer
}

func (s *closingSource) Close() error {
	err := s.Source.Close()
	if cerr := s.c.Close(); err == nil {
		err = cerr
	}
	return err
}

// readSchemaFile reads --schema: a schema-only dump whose CREATE TABLE
// statements stand in for the ones a data-only dump lacks. Its Schema()
// becomes dump.Options.Schema of the real input.
func readSchemaFile(ctx context.Context, path string) (*core.Schema, error) {
	src, err := openDumpFile(ctx, path, dump.Options{Name: filepath.Base(path)})
	if err != nil {
		return nil, err
	}
	for {
		if _, err := src.Next(ctx); err != nil {
			if !errors.Is(err, io.EOF) {
				_ = src.Close()
				return nil, err
			}
			break
		}
	}
	s := src.Schema()
	return s, src.Close()
}
