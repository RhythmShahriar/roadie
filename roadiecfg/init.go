// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package roadiecfg

import (
	"bytes"
	"embed"
	"fmt"
	"slices"
)

// templates holds the config init files: one for WordPress (WooCommerce
// too), one for Laravel and one for generic SQL. They are .tmpl so the
// header script leaves them alone: a user's settings file carries no
// copyright line.
//
//go:embed templates/*.yaml.tmpl
var templates embed.FS

// templateFor maps a stack to its template file.
var templateFor = map[string]string{
	"": "wordpress", "auto": "wordpress", "wordpress": "wordpress", "woocommerce": "wordpress",
	"laravel": "laravel", "sql": "sql",
}

// InitStacks lists the stacks config init --stack takes.
func InitStacks() []string { return []string{"wordpress", "woocommerce", "laravel", "sql"} }

func initTemplate(stack string) []byte {
	name, ok := templateFor[stack]
	if !ok {
		return nil
	}
	b, err := templates.ReadFile("templates/" + name + ".yaml.tmpl")
	if err != nil {
		panic(fmt.Sprintf("roadiecfg: embedded template %s: %v", name, err)) // the build embeds every file
	}
	return b
}

// InitMessage is the first line config init prints for stack, "Wrote
// .roadie/config.yaml for a WooCommerce site."
func InitMessage(stack string) string {
	what := map[string]string{
		"woocommerce": "a WooCommerce site", "laravel": "a Laravel app", "sql": "a generic SQL database",
	}[stack]
	if what == "" {
		what = "a WordPress site"
	}
	return "Wrote .roadie/config.yaml for " + what + "."
}

// GitExcludes are the paths config init keeps out of git (PRD 8.3).
func GitExcludes() []string {
	return []string{".roadie/reports/", ".roadie/key", ".roadie/backups/", ".roadie/basic-auth/"}
}

// WithSuggestions puts a Suggest block into a config init template, under
// the columns: example, so config init --from writes one file. An empty
// "columns: []" becomes "columns:", so uncommenting entries is enough.
func WithSuggestions(template, block []byte) []byte {
	if len(block) == 0 {
		return template
	}
	template = bytes.Replace(template, []byte("\ncolumns: []  "), []byte("\ncolumns:     "), 1)
	at := bytes.Index(template, []byte("\nkeys:\n"))
	if at < 0 {
		return append(slices.Clone(template), block...)
	}
	out := slices.Clone(template[:at+1])
	out = append(out, block...)
	out = append(out, '\n')
	return append(out, template[at+1:]...)
}
