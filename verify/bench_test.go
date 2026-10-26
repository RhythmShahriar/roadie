// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package verify

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/RhythmShahriar/roadie/core"
)

// benchMB is the size of the generated WooCommerce data BenchmarkVerify
// scans; ROADIE_BENCH_MB changes it.
const benchMB = 200

// benchRows is the rows per generated batch, about what a dump reader
// hands over at once.
const benchRows = 512

var (
	benchOnce    sync.Once
	benchBatches []*core.Batch
	benchBytes   int64
)

// wooData generates about mb megabytes of WooCommerce tables: posts with
// prose, links and prices, postmeta with order fields, options with
// serialized settings, comments and order addresses. People sit at
// reserved domains with one real-looking address in about 300 rows,
// so the aggregator has findings to group. The seed is fixed, so every
// run scans the same bytes.
func wooData(mb int) ([]*core.Batch, int64) {
	r := rand.New(rand.NewPCG(26, 10)) // #nosec G404 -- test data, not secrets
	posts := table("wp_posts", "ID", "post_title", "post_content", "guid")
	meta := table("wp_postmeta", "meta_id", "meta_key", "meta_value")
	opts := table("wp_options", "option_id", "option_name", "option_value")
	comments := table("wp_comments", "comment_ID", "comment_author", "comment_author_email", "comment_author_IP", "comment_content")
	addrs := table("wp_wc_order_addresses", "id", "first_name", "last_name", "address_1", "email", "phone")
	words := strings.Fields("bread flour oven crust loaf rye spelt butter honey seed oat bake fresh warm morning order delivery " +
		"shop parcel tracking basket label price total sourdough starter proof knead slice crumb taste")
	text := func(n int) string {
		var b strings.Builder
		for i := range n {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(words[r.IntN(len(words))])
		}
		return b.String()
	}
	email := func(i int) string {
		if i%307 == 0 {
			// A real-looking address now and then, built from parts.
			return "customer" + strconv.Itoa(i) + "@" + "gmail" + ".com"
		}
		return "customer" + strconv.Itoa(i) + "@example.com"
	}
	limit := int64(mb) << 20
	var out []*core.Batch
	var total int64
	id := 0
	add := func(t *core.Table, rows [][]string) {
		b := batch(t, rows...)
		for _, row := range b.Rows {
			for _, v := range row.Values {
				total += int64(len(v.Raw))
			}
		}
		out = append(out, b)
	}
	for total < limit {
		var p, m, o, c, a [][]string
		for range benchRows {
			id++
			n := strconv.Itoa(id)
			// Shares as in a WooCommerce dump: postmeta is the biggest
			// table, options a small one.
			switch k := id % 100; {
			case k < 25:
				p = append(p, []string{n, "Order " + n, fmt.Sprintf("<p>%s</p><p>Total £%d.%02d, SKU MSB-%05d, see https://wordpress.org/plugins/ and https://staging.shop.test/?p=%d</p><p>%s</p>",
					text(40), r.IntN(500), r.IntN(100), r.IntN(99999), id, text(60)), "https://staging.shop.test/?p=" + n})
			case k < 75:
				key := []string{"_billing_email", "_order_total", "_sku", "_billing_phone", "_customer_ip_address", "_date_paid"}[id%6]
				val := map[string]string{
					"_billing_email": email(id), "_order_total": fmt.Sprintf("%d.%02d", r.IntN(900), r.IntN(100)),
					"_sku": fmt.Sprintf("MSB-%05d", r.IntN(99999)), "_billing_phone": fmt.Sprintf("+44 7700 900%03d", r.IntN(1000)),
					"_customer_ip_address": fmt.Sprintf("192.0.2.%d", r.IntN(255)), "_date_paid": strconv.Itoa(1729900000 + id),
				}[key]
				m = append(m, []string{n, key, val})
			case k == 75:
				o = append(o, []string{n, "plugin_" + strconv.Itoa(r.IntN(400)) + "_settings", ser("enabled", "yes", "title", text(5), "endpoint", "https://api.example.com/v2", "limit", strconv.Itoa(r.IntN(100)))})
			case k < 90:
				c = append(c, []string{n, "Customer " + n, email(id), fmt.Sprintf("198.51.100.%d", r.IntN(255)), text(50)})
			default:
				a = append(a, []string{n, "Ada", "Kowalczyk", strconv.Itoa(r.IntN(200)) + " Mill Road", email(id), fmt.Sprintf("+1 212 555 01%02d", r.IntN(100))})
			}
		}
		add(posts, p)
		add(meta, m)
		add(opts, o)
		add(comments, c)
		add(addrs, a)
	}
	return out, total
}

// BenchmarkVerify scans about 200 MB of generated WooCommerce data with
// the real detectors, key rules and URL matcher on 8 workers and reports
// MB/s. CI runs it once (-benchtime 1x) as a smoke test; the measured
// speed is in changes/detect.md.
func BenchmarkVerify(b *testing.B) {
	mb := benchMB
	if s := os.Getenv("ROADIE_BENCH_MB"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			mb = n
		}
	}
	benchOnce.Do(func() { benchBatches, benchBytes = wooData(mb) })
	b.SetBytes(benchBytes)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		res, err := Run(context.Background(), Job{
			Source:     source(benchBatches...),
			Production: []string{"millstreet-bakery-demo.co.uk"},
			Opts:       Options{Jobs: 8},
		})
		if err != nil {
			b.Fatal(err)
		}
		if res.Coverage.BytesScanned == 0 || find(res, core.FindingPIIEmail) == nil {
			b.Fatalf("scanned %d bytes, findings %v", res.Coverage.BytesScanned, ids(res))
		}
	}
}
