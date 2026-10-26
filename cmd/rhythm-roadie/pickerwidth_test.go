// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

// A terminal that reports 0 columns gets the screen's width; a narrow
// one gets 40; any other keeps its own.
func TestFormWidth(t *testing.T) {
	for cols, want := range map[int]int{0: width, -1: width, 12: 40, 40: 40, 120: 120} {
		if got := formWidth(cols); got != want {
			t.Errorf("formWidth(%d) = %d, want %d", cols, got, want)
		}
	}
}

// The copy flow's first input draws at the width formWidth gives for a
// 0-column terminal; before, Huh's text input panicked there.
func TestPickerInputAtZeroColumns(t *testing.T) {
	var v string
	in := huh.NewInput().Title("Where is production?").Placeholder("ssh://deploy@shop.com/var/www/shop").Value(&v)
	f := huh.NewForm(huh.NewGroup(in)).WithTheme(huh.ThemeCharm()).WithWidth(formWidth(0))
	f.Init()
	// What a pty with no window size sends first.
	m, _ := f.Update(tea.WindowSizeMsg{Width: 0, Height: 0})
	if m.View() == "" {
		t.Error("empty view")
	}
}
