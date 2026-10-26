// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// readSchema reads every base table once per connection; nothing from a
// database is cached between runs.
func (c *Conn) readSchema(ctx context.Context) (*core.Schema, error) {
	if c.closed {
		return nil, errClosed
	}
	if c.schema != nil {
		return c.schema, nil
	}
	if c.busy {
		return nil, errBusy
	}
	s := &core.Schema{Dialect: c.engine, Server: c.server}
	var err error
	switch c.engine {
	case core.MySQL, core.MariaDB:
		err = c.mysqlSchema(ctx, s)
	case core.Postgres:
		err = c.pgSchema(ctx, s)
	case core.SQLite:
		err = c.sqliteSchema(ctx, s)
	}
	if err != nil {
		return nil, fmt.Errorf("db: read the schema of %s: %w", c.ep.Display, err)
	}
	s.Prefix = guessPrefix(s.Tables)
	c.schema = s
	return s, nil
}

// tableSet keeps tables in name order and finds them by name.
type tableSet struct {
	list  []*core.Table
	byKey map[string]*core.Table
}

func (t *tableSet) add(name string, rows int64) {
	if t.byKey == nil {
		t.byKey = map[string]*core.Table{}
	}
	tb := &core.Table{Name: name, RowsEstimate: max(rows, 0)}
	t.byKey[name] = tb
	t.list = append(t.list, tb)
}

// index is one unique index, or the primary key, of a table.
type index struct {
	primary bool
	unique  bool
	cols    []string
	partial bool // a column the index can't name: an expression or a predicate
}

// applyIndexes sets PrimaryKey and the Unique flag of every column that
// is a one-column unique index or primary key on its own.
func applyIndexes(tb *core.Table, idx []*index) {
	for _, ix := range idx {
		if ix.primary && !ix.partial {
			tb.PrimaryKey = ix.cols
		}
		if (ix.primary || ix.unique) && !ix.partial && len(ix.cols) == 1 {
			if i := tb.Col(ix.cols[0]); i >= 0 {
				tb.Columns[i].Unique = true
			}
		}
	}
}

func (c *Conn) mysqlSchema(ctx context.Context, s *core.Schema) error {
	var ts tableSet
	err := c.each(ctx, `SELECT TABLE_NAME, COALESCE(TABLE_ROWS, 0) FROM information_schema.TABLES
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE' ORDER BY TABLE_NAME`, func(r *sql.Rows) error {
		var name string
		var rows int64
		if err := r.Scan(&name, &rows); err != nil {
			return err
		}
		ts.add(name, rows)
		return nil
	})
	if err != nil {
		return err
	}
	err = c.each(ctx, `SELECT TABLE_NAME, COLUMN_NAME, LOWER(COLUMN_TYPE), IS_NULLABLE, COALESCE(CHARACTER_OCTET_LENGTH, 0), LOWER(DATA_TYPE), UPPER(EXTRA), COALESCE(COLLATION_NAME, '')
FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME, ORDINAL_POSITION`, func(r *sql.Rows) error {
		var table, name, typ, null, dataType, extra, coll string
		var octets int64
		if err := r.Scan(&table, &name, &typ, &null, &octets, &dataType, &extra, &coll); err != nil {
			return err
		}
		tb := ts.byKey[table]
		if tb == nil {
			return nil // a view
		}
		col := core.Column{Name: name, Type: typ, Nullable: null == "YES"}
		col.Generated = strings.Contains(extra, "VIRTUAL GENERATED") || strings.Contains(extra, "STORED GENERATED") || strings.Contains(extra, "PERSISTENT GENERATED")
		switch dataType {
		case "binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob":
			col.Binary = true
		case "char", "varchar", "tinytext", "text", "mediumtext", "longtext":
			col.Binary = coll == "binary"
		}
		switch dataType {
		case "binary", "varbinary", "tinyblob", "blob", "mediumblob", "longblob", "char", "varchar", "tinytext", "text", "mediumtext", "longtext":
			if octets > 0 && octets <= 1<<32 {
				col.MaxLen = int(octets)
			}
		}
		tb.Columns = append(tb.Columns, col)
		return nil
	})
	if err != nil {
		return err
	}
	idx := map[string]map[string]*index{}
	err = c.each(ctx, `SELECT TABLE_NAME, INDEX_NAME, NON_UNIQUE, COLUMN_NAME FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`, func(r *sql.Rows) error {
		var table, name string
		var nonUnique int
		var col sql.NullString
		if err := r.Scan(&table, &name, &nonUnique, &col); err != nil {
			return err
		}
		if idx[table] == nil {
			idx[table] = map[string]*index{}
		}
		ix := idx[table][name]
		if ix == nil {
			ix = &index{primary: name == "PRIMARY", unique: nonUnique == 0}
			idx[table][name] = ix
		}
		if !col.Valid {
			ix.partial = true // a functional key part
			return nil
		}
		ix.cols = append(ix.cols, col.String)
		return nil
	})
	if err != nil {
		return err
	}
	for _, tb := range ts.list {
		applyIndexes(tb, sortedIndexes(idx[tb.Name]))
	}
	s.Tables = ts.list
	return nil
}

func (c *Conn) pgSchema(ctx context.Context, s *core.Schema) error {
	var ts tableSet
	err := c.each(ctx, `SELECT c.relname, GREATEST(c.reltuples, 0)::bigint FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p') AND NOT c.relispartition ORDER BY c.relname`, func(r *sql.Rows) error {
		var name string
		var rows int64
		if err := r.Scan(&name, &rows); err != nil {
			return err
		}
		ts.add(name, rows)
		return nil
	})
	if err != nil {
		return err
	}
	err = c.each(ctx, `SELECT c.relname, a.attname, lower(format_type(a.atttypid, a.atttypmod)), NOT a.attnotnull, a.attgenerated <> ''
FROM pg_catalog.pg_attribute a
JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p') AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY c.relname, a.attnum`, func(r *sql.Rows) error {
		var table, name, typ string
		var null, gen bool
		if err := r.Scan(&table, &name, &typ, &null, &gen); err != nil {
			return err
		}
		tb := ts.byKey[table]
		if tb == nil {
			return nil
		}
		col := core.Column{Name: name, Type: typ, Nullable: null, Generated: gen}
		pgLimits(&col)
		tb.Columns = append(tb.Columns, col)
		return nil
	})
	if err != nil {
		return err
	}
	idx := map[string]map[string]*index{}
	err = c.each(ctx, `SELECT t.relname, i.indexrelid::text, i.indisprimary, i.indnatts, i.indpred IS NOT NULL OR i.indexprs IS NOT NULL, COALESCE(a.attname, '')
FROM pg_catalog.pg_index i
JOIN pg_catalog.pg_class t ON t.oid = i.indrelid
JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace
CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord)
LEFT JOIN pg_catalog.pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum AND k.attnum > 0
WHERE n.nspname = current_schema() AND i.indisunique
ORDER BY t.relname, i.indexrelid, k.ord`, func(r *sql.Rows) error {
		var table, name, col string
		var primary, partial bool
		var natts int
		if err := r.Scan(&table, &name, &primary, &natts, &partial, &col); err != nil {
			return err
		}
		if idx[table] == nil {
			idx[table] = map[string]*index{}
		}
		ix := idx[table][name]
		if ix == nil {
			ix = &index{primary: primary, unique: true, partial: partial}
			idx[table][name] = ix
		}
		if col == "" {
			ix.partial = true
			return nil
		}
		// INCLUDE columns follow the key columns and aren't part of the key.
		if len(ix.cols) < natts {
			ix.cols = append(ix.cols, col)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, tb := range ts.list {
		applyIndexes(tb, sortedIndexes(idx[tb.Name]))
	}
	s.Tables = ts.list
	return nil
}

// pgLimits sets MaxLen (characters x 4) and Binary as the dump reader
// does for the same type.
func pgLimits(c *core.Column) {
	if c.Type == "bytea" {
		c.Binary = true
		return
	}
	for _, p := range []string{"character varying(", "character("} {
		if rest, ok := strings.CutPrefix(c.Type, p); ok {
			n, _, _ := strings.Cut(rest, ")")
			if v, err := strconv.Atoi(n); err == nil && v > 0 {
				c.MaxLen = v * 4
			}
		}
	}
}

func (c *Conn) sqliteSchema(ctx context.Context, s *core.Schema) error {
	var ts tableSet
	err := c.each(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY name`, func(r *sql.Rows) error {
		var name string
		if err := r.Scan(&name); err != nil {
			return err
		}
		ts.add(name, 0)
		return nil
	})
	if err != nil {
		return err
	}
	for _, tb := range ts.list {
		var pk []struct {
			n    int
			name string
		}
		q := "SELECT name, lower(type), \"notnull\", pk, hidden FROM pragma_table_xinfo(?) ORDER BY cid"
		err := c.each(ctx, q, func(r *sql.Rows) error {
			var name, typ string
			var notNull, pkn, hidden int
			if err := r.Scan(&name, &typ, &notNull, &pkn, &hidden); err != nil {
				return err
			}
			col := core.Column{Name: name, Type: typ, Nullable: notNull == 0 && pkn == 0, Generated: hidden == 2 || hidden == 3}
			if hidden == 1 {
				return nil // a hidden column of a virtual table
			}
			col.Binary = strings.Contains(typ, "blob")
			for _, p := range []string{"varchar(", "character varying(", "char(", "character(", "nvarchar("} {
				if rest, ok := strings.CutPrefix(typ, p); ok {
					n, _, _ := strings.Cut(rest, ")")
					if v, err := strconv.Atoi(strings.TrimSpace(n)); err == nil && v > 0 {
						col.MaxLen = v * 4
					}
					break
				}
			}
			if pkn > 0 {
				pk = append(pk, struct {
					n    int
					name string
				}{pkn, name})
			}
			tb.Columns = append(tb.Columns, col)
			return nil
		}, tb.Name)
		if err != nil {
			return err
		}
		sort.Slice(pk, func(i, j int) bool { return pk[i].n < pk[j].n })
		var idx []*index
		if len(pk) > 0 {
			ix := &index{primary: true, unique: true}
			for _, p := range pk {
				ix.cols = append(ix.cols, p.name)
			}
			idx = append(idx, ix)
		}
		var uniques []string
		err = c.each(ctx, `SELECT name FROM pragma_index_list(?) WHERE "unique" = 1 AND partial = 0 AND origin <> 'pk' ORDER BY name`, func(r *sql.Rows) error {
			var name string
			if err := r.Scan(&name); err != nil {
				return err
			}
			uniques = append(uniques, name)
			return nil
		}, tb.Name)
		if err != nil {
			return err
		}
		for _, u := range uniques {
			ix := &index{unique: true}
			err := c.each(ctx, `SELECT COALESCE(name, '') FROM pragma_index_info(?) ORDER BY seqno`, func(r *sql.Rows) error {
				var name string
				if err := r.Scan(&name); err != nil {
					return err
				}
				if name == "" {
					ix.partial = true
				}
				ix.cols = append(ix.cols, name)
				return nil
			}, u)
			if err != nil {
				return err
			}
			idx = append(idx, ix)
		}
		applyIndexes(tb, idx)
	}
	s.Tables = ts.list
	return nil
}

// sortedIndexes lists the indexes of one table in name order, so the
// result doesn't depend on map order.
func sortedIndexes(m map[string]*index) []*index {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]*index, 0, len(m))
	for _, n := range names {
		out = append(out, m[n])
	}
	return out
}

// each runs a query on the session and calls f for every row.
func (c *Conn) each(ctx context.Context, q string, f func(*sql.Rows) error, args ...any) error {
	rows, err := c.conn.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := f(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// guessPrefix is the WordPress table prefix: of the prefixes that end in
// '_' and come before "options" in a table name, the one most tables
// start with. Without an options table there is none.
func guessPrefix(tables []*core.Table) string {
	best, bestN := "", 0
	for _, t := range tables {
		p, ok := strings.CutSuffix(t.Name, "options")
		if !ok || p == "" || !strings.HasSuffix(p, "_") {
			continue
		}
		n := 0
		for _, u := range tables {
			if strings.HasPrefix(u.Name, p) {
				n++
			}
		}
		if n > bestN || n == bestN && len(p) < len(best) {
			best, bestN = p, n
		}
	}
	return best
}
