// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Command gen writes r17's large dumps (package testbed/gen) and adds
// their canaries to canaries.json:
//
//	go run ./testbed/gen/cmd/gen -size 2GB -small 200MB -out testbed/runtime/fixtures/r17 -runtime testbed/runtime
//
// It writes db.sql.gz (-size of SQL) and db-small.sql.gz (-small, for the
// memory comparison), serialized.json and serialized-small.json (a sample
// of the serialized cells for the PHP integrity check) and gen.json (sizes,
// rows and each canary's offset, by ID). It prints sizes and counts, never
// a canary value. Exit codes: 0 written, 1 writing failed, 2 bad flags.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/klauspost/compress/gzip"

	"github.com/RhythmShahriar/roadie/testbed/gen"
	"github.com/RhythmShahriar/roadie/testbed/oracle"
	"github.com/RhythmShahriar/roadie/testbed/seed"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// Manifest is gen.json.
type Manifest struct {
	Seed  int64                  `json:"seed"`
	Files map[string]*gen.Result `json:"files"`
}

func run(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("gen", flag.ContinueOnError)
	fl.SetOutput(stderr)
	sizeFlag := fl.String("size", "2GB", "SQL bytes of db.sql.gz (before compression)")
	smallFlag := fl.String("small", "200MB", `SQL bytes of db-small.sql.gz; "" leaves it out`)
	out := fl.String("out", "testbed/runtime/fixtures/r17", "where the dumps go")
	runtimeDir := fl.String("runtime", "testbed/runtime", "the folder of canaries.json")
	seedFlag := fl.Int64("seed", 1, "the seed of the data and the canaries")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	files := []struct{ name, serialized, size string }{{"db-small.sql.gz", "serialized-small.json", *smallFlag}, {"db.sql.gz", "serialized.json", *sizeFlag}}
	if *smallFlag == "" {
		files = files[1:]
	}
	sizes := make([]int64, len(files))
	for i, f := range files {
		n, err := gen.ParseSize(f.size)
		if err != nil {
			fmt.Fprintln(stderr, "gen:", err)
			return 2
		}
		sizes[i] = n
	}
	if err := os.MkdirAll(*out, 0o700); err != nil {
		fmt.Fprintln(stderr, "gen:", err)
		return 1
	}
	// One value maker for both files keeps every canary ID unique.
	values := seed.NewValues(*seedFlag, gen.Scenario)
	m := Manifest{Seed: *seedFlag, Files: map[string]*gen.Result{}}
	var all []seed.Canary
	for i, f := range files {
		res, err := writeGzip(filepath.Join(*out, f.name), gen.Options{Size: sizes[i], Seed: *seedFlag + int64(i), Values: values})
		if err != nil {
			fmt.Fprintln(stderr, "gen:", err)
			return 1
		}
		if err := writeJSON(filepath.Join(*out, f.serialized), oracle.SerializedFile{Version: 1, Cells: res.Serialized}); err != nil {
			fmt.Fprintln(stderr, "gen:", err)
			return 1
		}
		m.Files[f.name] = res
		all = append(all, res.Canaries...)
		fmt.Fprintf(stdout, "%-16s %13d bytes of SQL, %d canaries, %d serialized cells sampled\n", f.name, res.Bytes, len(res.Canaries), len(res.Serialized))
	}
	if err := writeJSON(filepath.Join(*out, "gen.json"), m); err != nil {
		fmt.Fprintln(stderr, "gen:", err)
		return 1
	}
	if err := seed.MergeCanaries(*runtimeDir, *seedFlag, gen.Scenario, all); err != nil {
		fmt.Fprintln(stderr, "gen:", err)
		return 1
	}
	fmt.Fprintf(stdout, "%d canaries of %s in %s\n", len(all), gen.Scenario, filepath.Join(*runtimeDir, "canaries.json"))
	return 0
}

// writeGzip writes one dump through gzip into path, atomically.
func writeGzip(path string, o gen.Options) (*gen.Result, error) {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec G304 -- the operator's output folder
	if err != nil {
		return nil, err
	}
	zw, _ := gzip.NewWriterLevel(f, gzip.BestSpeed)
	res, err := gen.Write(zw, o)
	if cerr := zw.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	return res, os.Rename(tmp, path)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
