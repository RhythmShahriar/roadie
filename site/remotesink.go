// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package site

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sync"

	"github.com/RhythmShahriar/roadie/core"
	"github.com/RhythmShahriar/roadie/site/remote"
)

// TableCounter is implemented by the sinks of ssh and alias targets: the
// engine passes the number of tables the copy holds, so a broken import
// can say "after 37 of 61 tables".
type TableCounter interface {
	ExpectTables(n int)
}

// remoteSink feeds a dump writer into a remote import (wp db import -,
// mysql or psql) and maps how the import ended to PRD 17.
type remoteSink struct {
	inner core.Sink
	pw    *io.PipeWriter
	done  chan error
	dest  string
	what  string

	mu       sync.Mutex
	tables   int // CREATE TABLE statements written
	drops    int // DROP TABLE statements written
	expected int
	state    core.TargetState
	ended    bool
	endErr   error
}

// ExpectTables sets the total for the import-mixed message.
func (s *remoteSink) ExpectTables(n int) {
	s.mu.Lock()
	s.expected = n
	s.mu.Unlock()
}

func (s *remoteSink) Write(ctx context.Context, b *core.Batch) error {
	if err := s.inner.Write(ctx, b); err != nil {
		_ = s.pw.CloseWithError(err)
		return s.fail(err)
	}
	if b.Pass != nil && (b.Pass.Kind == core.StmtCreateTable || b.Pass.Kind == core.StmtDropTable) {
		s.mu.Lock()
		if b.Pass.Kind == core.StmtCreateTable {
			s.tables++
		} else {
			s.drops++
		}
		s.state = core.Mixed
		s.mu.Unlock()
	}
	return nil
}

// Commit finishes the stream and waits for the import to exit.
func (s *remoteSink) Commit(ctx context.Context) error {
	if err := s.inner.Commit(ctx); err != nil {
		_ = s.pw.CloseWithError(err)
		return s.fail(err)
	}
	_ = s.pw.Close()
	if err := s.wait(); err != nil {
		return s.mapErr(err)
	}
	s.mu.Lock()
	s.state = core.Replaced
	s.mu.Unlock()
	return nil
}

// Abort stops the import: the remote client sees its input end early.
// Tables it already replaced stay replaced, so the state is Mixed then.
func (s *remoteSink) Abort(ctx context.Context) error {
	err := s.inner.Abort(ctx)
	_ = s.pw.CloseWithError(errAborted)
	_ = s.wait()
	return err
}

// errAborted ends the remote import's input on Abort.
var errAborted = errors.New("roadie stopped the import")

func (s *remoteSink) State() core.TargetState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// wait returns how the remote command ended, once.
func (s *remoteSink) wait() error {
	s.mu.Lock()
	if s.ended {
		defer s.mu.Unlock()
		return s.endErr
	}
	s.mu.Unlock()
	err := <-s.done
	s.mu.Lock()
	s.ended, s.endErr = true, err
	s.mu.Unlock()
	return err
}

// fail maps a write error: the import's own exit explains a broken pipe.
func (s *remoteSink) fail(writeErr error) error {
	if err := s.wait(); err != nil {
		return s.mapErr(err)
	}
	return s.mapErr(writeErr)
}

var (
	collationRE = regexp.MustCompile(`(?:ERROR 1273|Unknown collation)[^\n']*'([A-Za-z0-9_]+)'|collation "([A-Za-z0-9_.-]+)" for encoding`)
	mysqlCodeRE = regexp.MustCompile(`ERROR ([0-9]{4})`)
)

// mapErr turns how the import ended into *core.Error. The client's
// stderr is never quoted, since a MySQL error can echo a row value; only
// its error number is.
func (s *remoteSink) mapErr(err error) error {
	s.mu.Lock()
	k, n, drops := s.tables, s.expected, s.drops
	s.mu.Unlock()
	var re *remote.RunError
	isRun := errors.As(err, &re)
	if isRun {
		if m := collationRE.FindSubmatch(re.Stderr); m != nil {
			coll := string(m[1]) + string(m[2])
			// The failing CREATE is the k-th statement, so the target is
			// untouched only when it is the first and no DROP TABLE went
			// ahead of it: a dump that drops each table before creating
			// it has already removed one.
			if k <= 1 && drops == 0 {
				s.setState(core.Untouched)
				return &core.Error{Code: core.ErrImport, Exit: 3, State: core.Untouched, Msg: fmt.Sprintf(
					"import: the database on %s doesn't know collation %s. Run again with --fix-collation. The target is untouched.", s.dest, coll)}
			}
			s.setState(core.Mixed)
			return &core.Error{Code: core.ErrImport, Exit: 3, State: core.Mixed, Msg: fmt.Sprintf(
				"import: the database on %s doesn't know collation %s, after %s. The target is in a mixed state: some tables are pseudonymised, the rest still hold the earlier copy. Run again with --fix-collation.", s.dest, coll, tableCount(max(k-1, 0), n))}
		}
	}
	var ce *core.Error
	if k == 0 && drops == 0 {
		s.setState(core.Untouched)
		if errors.As(err, &ce) {
			return &core.Error{Code: ce.Code, Exit: ce.Exit, Msg: ce.Msg, State: core.Untouched}
		}
		return &core.Error{Code: core.ErrImport, Exit: 3, State: core.Untouched, Msg: fmt.Sprintf(
			"import: %s on %s failed%s before any table was replaced. The target is untouched.", s.what, s.dest, why(re))}
	}
	s.setState(core.Mixed)
	how := fmt.Sprintf("the connection to %s was lost", s.dest)
	if isRun {
		how = fmt.Sprintf("%s on %s failed%s", s.what, s.dest, why(re))
	}
	return &core.Error{Code: core.ErrImportMixed, Exit: 3, State: core.Mixed, Msg: fmt.Sprintf(
		"import: %s after %s. The target is in a mixed state: some tables are pseudonymised, the rest still hold the earlier copy. Run the same command again.", how, tableCount(k, n))}
}

func (s *remoteSink) setState(st core.TargetState) {
	s.mu.Lock()
	s.state = st
	s.mu.Unlock()
}

// why is " (MySQL ERROR 1062)" or " (exit 1)" for a failed remote command.
func why(re *remote.RunError) string {
	if re == nil {
		return ""
	}
	if m := mysqlCodeRE.FindSubmatch(re.Stderr); m != nil {
		return " (MySQL ERROR " + string(m[1]) + ")"
	}
	return fmt.Sprintf(" (exit %d)", re.Exit)
}

// tableCount is "37 of 61 tables", or "37 tables" with no total.
func tableCount(k, n int) string {
	unit := "tables"
	if k == 1 && n == 0 {
		unit = "table"
	}
	if n > 0 {
		return fmt.Sprintf("%d of %d %s", k, n, unit)
	}
	return fmt.Sprintf("%d %s", k, unit)
}
