// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"

	"github.com/RhythmShahriar/rhythm/kit/cli"
)

// explainFS holds one text per finding ID, error code and rule family
// (profile), written from PRD 13, 15.2 and 17, plus "pseudonymised" with
// the PRD 19.1 footer. explain works offline from these.
//
//go:embed explain/*.md
var explainFS embed.FS

// explainIDs lists every ID explain knows, sorted.
func explainIDs() []string {
	entries, _ := fs.ReadDir(explainFS, "explain")
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, strings.TrimSuffix(e.Name(), ".md"))
	}
	slices.Sort(ids)
	return ids
}

// explainText finds the text for id: the ID itself, else a rule ID's
// family ("woocommerce.wc_orders.billing_email" reads "woocommerce").
func explainText(id string) (string, string, bool) {
	for try := id; try != ""; {
		if b, err := explainFS.ReadFile("explain/" + try + ".md"); err == nil && !strings.ContainsAny(try, "/\\") {
			return try, string(b), true
		}
		i := strings.LastIndex(try, ".")
		if i < 0 {
			break
		}
		try = try[:i]
	}
	return "", "", false
}

// runExplain is explain <id> (PRD 6.10).
func runExplain(_ context.Context, p *parsed, stdout, stderr io.Writer) error {
	if len(p.args) != 1 {
		return usageError(stderr, "explain", "explain takes one ID, e.g. rhythm-roadie explain uncovered-column")
	}
	id := strings.TrimSpace(p.args[0])
	found, text, ok := explainText(id)
	if !ok {
		msg := fmt.Sprintf("no explanation for %q", id)
		if s := cli.Suggest(id, explainIDs()); s != "" {
			msg += "\n  did you mean: " + prog() + " explain " + s
		}
		return usageError(stderr, "explain", "%s", msg)
	}
	if p.f.g.json {
		return writeEnvelope(stdout, "explain", map[string]string{"id": found, "text": text})
	}
	_, err := io.WriteString(stdout, text)
	return err
}
