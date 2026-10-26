// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package roadiecfg

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// gitDirs finds the work tree root at or above dir and the folder git
// reads info/exclude from. In a clone that is .git itself; in a linked
// worktree .git is a file ("gitdir: <path>") naming a folder whose
// commondir file points at the main .git; info/exclude lives there.
// ok is false outside a work tree.
func gitDirs(dir string) (root, common string, ok bool, err error) {
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", "", false, err
	}
	for {
		dotGit := filepath.Join(dir, ".git")
		fi, err := os.Stat(dotGit)
		switch {
		case err == nil && fi.IsDir():
			return dir, dotGit, true, nil
		case err == nil:
			gitdir, err := readGitFile(dotGit)
			if err != nil {
				return "", "", false, err
			}
			return dir, commonDir(gitdir), true, nil
		case !errors.Is(err, fs.ErrNotExist):
			return "", "", false, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false, nil
		}
		dir = parent
	}
}

// readGitFile reads the gitdir a .git file names, relative to the file.
func readGitFile(file string) (string, error) {
	b, err := os.ReadFile(file) // #nosec G304 -- the .git file of the user's own work tree
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(string(b), "\n")
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	if !ok {
		return "", fmt.Errorf("%s doesn't name a gitdir", file)
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(filepath.Dir(file), gitdir)
	}
	return gitdir, nil
}

// commonDir follows a worktree gitdir's commondir file, when it has one.
func commonDir(gitdir string) string {
	b, err := os.ReadFile(filepath.Join(gitdir, "commondir")) // #nosec G304 -- inside the user's own git folder
	if err != nil {
		return gitdir
	}
	c := strings.TrimSpace(string(b))
	if !filepath.IsAbs(c) {
		c = filepath.Join(gitdir, c)
	}
	return filepath.Clean(c)
}

func excludeFromGit(dir string, paths []string) (bool, error) {
	root, common, ok, err := gitDirs(dir)
	if err != nil || !ok {
		return false, err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false, err
	}
	file := filepath.Join(common, "info", "exclude")
	old, err := os.ReadFile(file) // #nosec G304 -- the work tree's own exclude file
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	have := map[string]bool{}
	for _, l := range strings.Split(string(old), "\n") {
		have[strings.TrimSpace(l)] = true
	}
	var add []string
	for _, p := range paths {
		// Anchored at the work tree root, so a folder of the same name
		// deeper down isn't hidden too.
		line := "/" + path.Join(filepath.ToSlash(rel), p)
		if strings.HasSuffix(p, "/") {
			line += "/"
		}
		if !have[line] {
			add = append(add, line)
			have[line] = true
		}
	}
	if len(add) == 0 {
		return false, nil
	}
	out := bytes.Clone(old)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	out = append(out, "# rhythm-roadie: reports, keys and backups stay out of git\n"...)
	out = append(out, strings.Join(add, "\n")+"\n"...)
	if err := os.MkdirAll(filepath.Dir(file), 0o750); err != nil {
		return false, err
	}
	if err := os.WriteFile(file, out, 0o644); err != nil { // #nosec G306 G703 -- git's own file, readable as git makes it
		return false, err
	}
	return true, nil
}
