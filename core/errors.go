// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package core

import "fmt"

// Error is a run that stopped, with its PRD 17 code and exit code. Msg is
// the full line printed to the user and never holds a raw data value.
type Error struct {
	Code  string      `json:"code"`
	Exit  int         `json:"exit"`
	Msg   string      `json:"message"`
	State TargetState `json:"-"` // the target's state when the run stopped
	Line  int64       `json:"-"` // input line, when the error points at one
}

func (e *Error) Error() string { return e.Msg }

// Is matches another *Error with the same code, so errors.Is(err,
// ErrNotBuilt) holds for a copy or a wrapped one too.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code != "" && t.Code == e.Code
}

// Errorf builds an *Error whose message is the formatted text.
func Errorf(code string, exit int, format string, a ...any) *Error {
	return &Error{Code: code, Exit: exit, Msg: fmt.Sprintf(format, a...)}
}

// ErrNotBuilt is what a frozen API stub returns until its lane replaces it.
var ErrNotBuilt = &Error{Code: ErrCodeNotBuilt, Exit: 3, Msg: "not built yet"}
