// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"context"
	"io"
	"io/fs"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/db"
	"github.com/RhythmShahriar/roadie/site/killswitch"
)

// capture keeps, while a dump or a database streams by, the few rows the
// kill switch's data checks read (killswitch.DataRead), so those checks
// run on a target with no files without reading it again.
type capture struct {
	batches map[string][]*core.Batch // by table name
	reads   map[string]*capRead      // by table name; nil for a table no check reads
}

// capRead is what the checks read of one table.
type capRead struct {
	cols []string
	keep func([][]byte) bool
}

func newCapture() *capture {
	return &capture{batches: map[string][]*core.Batch{}, reads: map[string]*capRead{}}
}

// readOf returns what the checks read of table, by its name's ending.
func (c *capture) readOf(table string) *capRead {
	if rd, ok := c.reads[table]; ok {
		return rd
	}
	var rd *capRead
	for _, base := range killswitch.DataTables {
		if strings.HasSuffix(table, base) {
			cols, keep := killswitch.DataRead(base)
			rd = &capRead{cols: cols, keep: keep}
			break
		}
	}
	c.reads[table] = rd
	return rd
}

// add keeps the rows of b the checks read, copied, with only their columns.
func (c *capture) add(b *core.Batch) {
	rd := c.readOf(b.Table.Name)
	if rd == nil {
		return
	}
	pos := make([]int, len(rd.cols))
	cols := make([]int, len(rd.cols))
	for i, name := range rd.cols {
		ti := b.Table.Col(name)
		cols[i], pos[i] = ti, -1
		if ti < 0 {
			continue
		}
		if len(b.Columns) == 0 {
			pos[i] = ti
			continue
		}
		for p, x := range b.Columns {
			if x == ti {
				pos[i] = p
				break
			}
		}
	}
	for _, ti := range cols {
		if ti < 0 {
			// A table of that name without the columns isn't the one the
			// checks mean.
			return
		}
	}
	out := &core.Batch{Table: b.Table, Columns: cols}
	vals := make([][]byte, len(pos))
	for _, row := range b.Rows {
		if row.Drop {
			continue
		}
		kept := make([]core.Value, len(pos))
		for i, p := range pos {
			vals[i] = nil
			if p >= 0 && p < len(row.Values) && row.Values[p].Kind != core.Null {
				vals[i] = row.Values[p].Raw
				kept[i] = core.Value{Kind: row.Values[p].Kind, Raw: append([]byte(nil), vals[i]...)}
			} else {
				kept[i] = core.Value{Kind: core.Null}
			}
		}
		if rd.keep(vals) {
			out.Rows = append(out.Rows, core.Row{Values: kept})
		}
	}
	c.batches[b.Table.Name] = append(c.batches[b.Table.Name], out)
}

// dataSite is a target with no files, a dump or a database, as the kill
// switch's data checks see it: its captured rows and nothing else.
type dataSite struct {
	ep     core.Endpoint
	stack  core.Stack
	prefix string
	has    map[string]bool
	cap    *capture
}

// newDataSite builds the site from the captured rows and every table the
// stream saw. The table prefix is that of the shortest options table: a
// multisite's main one.
func newDataSite(ep core.Endpoint, stack core.Stack, tables []*core.Table, c *capture) *dataSite {
	s := &dataSite{ep: ep, stack: stack, has: map[string]bool{}, cap: c}
	best := ""
	for _, t := range tables {
		s.has[t.Name] = true
		if strings.HasSuffix(t.Name, "options") && t.Col("option_name") >= 0 && t.Col("option_value") >= 0 &&
			(best == "" || len(t.Name) < len(best)) {
			best = t.Name
		}
	}
	if best != "" {
		s.prefix = strings.TrimSuffix(best, "options")
	}
	return s
}

func (s *dataSite) Endpoint() core.Endpoint { return s.ep }
func (s *dataSite) Stack() core.Stack       { return s.stack }
func (s *dataSite) ReadOnly() bool          { return true }
func (s *dataSite) ReadFile(context.Context, string) ([]byte, error) {
	return nil, fs.ErrNotExist
}
func (s *dataSite) WriteFile(context.Context, string, []byte, fs.FileMode) error {
	return core.ErrReadOnly
}
func (s *dataSite) Remove(context.Context, string) error                       { return core.ErrReadOnly }
func (s *dataSite) Exists(context.Context, string) (bool, error)               { return false, nil }
func (s *dataSite) List(context.Context, string, int) ([]core.FileInfo, error) { return nil, nil }
func (s *dataSite) WP(context.Context, ...string) ([]byte, error)              { return nil, core.ErrNoWPCLI }
func (s *dataSite) DBIdentity(context.Context) (core.DBIdentity, error) {
	return core.DBIdentity{Prefix: s.prefix}, nil
}
func (s *dataSite) Sink(context.Context) (core.Sink, error) { return nil, core.ErrReadOnly }
func (s *dataSite) RemoteCommands() []string                { return nil }
func (s *dataSite) Backup(context.Context) (string, string, error) {
	return "", "", core.ErrReadOnly
}
func (s *dataSite) Close() error { return nil }

// Source gives the captured rows of the named tables; a table the stream
// never saw is a db.NoTableError, which the checks read as "not there".
func (s *dataSite) Source(_ context.Context, tables []string) (core.Source, error) {
	var out []*core.Batch
	for _, t := range tables {
		if !s.has[t] {
			return nil, &db.NoTableError{DB: s.ep.Display, Table: t}
		}
		out = append(out, s.cap.batches[t]...)
	}
	return &sliceSource{batches: out}, nil
}

// sliceSource streams batches already in memory.
type sliceSource struct {
	batches []*core.Batch
	next    int
}

func (s *sliceSource) Schema() *core.Schema { return nil }
func (s *sliceSource) Next(context.Context) (*core.Batch, error) {
	if s.next >= len(s.batches) {
		return nil, io.EOF
	}
	s.next++
	return s.batches[s.next-1], nil
}
func (s *sliceSource) Fingerprint() core.Fingerprint { return core.Fingerprint{} }
func (s *sliceSource) Close() error                  { return nil }

var (
	_ core.Site   = (*dataSite)(nil)
	_ core.Source = (*sliceSource)(nil)
)
