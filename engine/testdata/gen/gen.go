// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package gen generates a WooCommerce-shaped MySQL dump of a given size
// for the engine's benchmarks and determinism tests: users and their
// meta, HPOS orders with addresses and meta, order notes, Action
// Scheduler actions and options with serialized gateway settings
// holding a live-looking key. Every person is made up: emails are on
// example.com, phones in Ofcom's fictional 07700 900 range and IPs in
// the documentation ranges. The same options give the same bytes.
package gen

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"iter"
	"math/rand/v2"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/roadie/core"
)

// Options says what to generate.
type Options struct {
	Size    int64  // about this many bytes of SQL; at least one customer
	Seed    uint64 // the same seed gives the same dump
	LiveKey string // a live Stripe-shaped key, assembled by the caller
	Prefix  string // the WordPress table prefix; "" means wp_
}

func (o Options) prefix() string {
	if o.Prefix == "" {
		return "wp_"
	}
	return o.Prefix
}

// Record is one row of one table.
type Record struct {
	Table  *core.Table
	Values []core.Value
}

// col builds a column from "name:type"; text columns default to
// varchar(255) and a trailing "?" makes it nullable.
func col(spec string) core.Column {
	name, typ, _ := strings.Cut(spec, ":")
	c := core.Column{Name: name, Type: "varchar(255)"}
	if typ != "" {
		c.Type = typ
	}
	if strings.HasSuffix(c.Type, "?") {
		c.Type, c.Nullable = strings.TrimSuffix(c.Type, "?"), true
	}
	if n, ok := strings.CutPrefix(c.Type, "varchar("); ok {
		if v, err := strconv.Atoi(strings.TrimSuffix(n, ")")); err == nil {
			c.MaxLen = v * 4
		}
	}
	return c
}

func table(name string, pk string, cols ...string) *core.Table {
	t := &core.Table{Name: name, PrimaryKey: []string{pk}}
	for _, c := range cols {
		t.Columns = append(t.Columns, col(c))
	}
	return t
}

// Tables returns the dump's tables in dump order (mysqldump sorts them
// by name).
func Tables(prefix string) []*core.Table {
	if prefix == "" {
		prefix = "wp_"
	}
	return []*core.Table{
		table(prefix+"actionscheduler_actions", "action_id", "action_id:bigint(20) unsigned", "hook:varchar(191)", "status:varchar(20)",
			"args:varchar(191)?", "extended_args:varchar(8000)?"),
		table(prefix+"comments", "comment_ID", "comment_ID:bigint(20) unsigned", "comment_post_ID:bigint(20) unsigned", "comment_author:tinytext",
			"comment_author_email:varchar(100)", "comment_author_IP:varchar(100)", "comment_content:text", "comment_type:varchar(20)"),
		table(prefix+"options", "option_id", "option_id:bigint(20) unsigned", "option_name:varchar(191)", "option_value:longtext", "autoload:varchar(20)"),
		table(prefix+"usermeta", "umeta_id", "umeta_id:bigint(20) unsigned", "user_id:bigint(20) unsigned", "meta_key:varchar(255)?", "meta_value:longtext?"),
		table(prefix+"users", "ID", "ID:bigint(20) unsigned", "user_login:varchar(60)", "user_pass:varchar(255)", "user_nicename:varchar(50)",
			"user_email:varchar(100)", "user_url:varchar(100)", "user_registered:datetime", "user_activation_key:varchar(255)",
			"user_status:int(11)", "display_name:varchar(250)"),
		table(prefix+"wc_order_addresses", "id", "id:bigint(20) unsigned", "order_id:bigint(20) unsigned", "address_type:varchar(20)?",
			"first_name:text?", "last_name:text?", "company:text?", "address_1:text?", "address_2:text?", "city:text?", "state:text?",
			"postcode:text?", "country:text?", "email:varchar(320)?", "phone:varchar(100)?"),
		table(prefix+"wc_orders", "id", "id:bigint(20) unsigned", "status:varchar(20)?", "currency:varchar(10)?", "type:varchar(20)?",
			"total_amount:decimal(26,8)?", "customer_id:bigint(20) unsigned?", "billing_email:varchar(320)?", "date_created_gmt:datetime?",
			"ip_address:varchar(100)?", "user_agent:text?", "customer_note:text?", "transaction_id:varchar(100)?"),
		table(prefix+"wc_orders_meta", "id", "id:bigint(20) unsigned", "order_id:bigint(20) unsigned?", "meta_key:varchar(255)?", "meta_value:text?"),
	}
}

// Made-up name parts; combined they give thousands of people.
var (
	firstNames = []string{"Anna", "Ben", "Clara", "Dev", "Elif", "Farid", "Greta", "Hugo", "Ines", "Jonas", "Kofi", "Lena", "Mateo", "Nora", "Omar", "Priya"}
	lastNames  = []string{"Lindqvist", "Okafor", "Moreau", "Sato", "Brennan", "Kowalczyk", "Haddad", "Ferreira", "Novak", "Achterberg", "Quinlan", "Varga"}
	streets    = []string{"Mill Lane", "Station Road", "Riverside", "Orchard Close", "Kingfisher Way", "Elm Grove"}
	cities     = []string{"Exampleton", "Testbury", "Sampleford", "Demoville"}
	agents     = []string{"Mozilla/5.0 (X11; Linux x86_64) Firefox/131.0", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Safari/604.1"}
)

// person is one made-up customer.
type person struct {
	id                    int
	first, last, email    string
	phone, ip, street     string
	city, postcode, login string
}

func newPerson(r *rand.Rand, id int) person {
	f, l := firstNames[r.IntN(len(firstNames))], lastNames[r.IntN(len(lastNames))]
	return person{
		id: id, first: f, last: l,
		login:    strings.ToLower(f) + strconv.Itoa(id),
		email:    strings.ToLower(f+"."+l) + strconv.Itoa(id) + "@example.com",
		phone:    fmt.Sprintf("+44 7700 900%03d", r.IntN(1000)),
		ip:       fmt.Sprintf("198.51.100.%d", 1+r.IntN(254)),
		street:   fmt.Sprintf("%d %s", 1+r.IntN(200), streets[r.IntN(len(streets))]),
		city:     cities[r.IntN(len(cities))],
		postcode: fmt.Sprintf("ZZ%d %dXX", 1+r.IntN(99), r.IntN(10)),
	}
}

func str(s string) core.Value { return core.Value{Kind: core.String, Raw: []byte(s)} }
func num(n int) core.Value    { return core.Value{Kind: core.Number, Raw: []byte(strconv.Itoa(n))} }
func null() core.Value        { return core.Value{Kind: core.Null} }

// phpString is a PHP serialized string.
func phpString(s string) string { return "s:" + strconv.Itoa(len(s)) + `:"` + s + `";` }

// perCustomer is about how many bytes of SQL one customer adds; it sets
// how many customers a size needs.
const perCustomer = 3600

// customers is how many customers make about Size bytes.
func (o Options) customers() int {
	return max(1, int(o.Size/perCustomer))
}

// Records yields every row in dump order: table by table, each table's
// rows by primary key.
func Records(o Options) iter.Seq[Record] {
	return func(yield func(Record) bool) {
		ts := Tables(o.prefix())
		n := o.customers()
		people := func() iter.Seq[person] {
			return func(y func(person) bool) {
				r := rand.New(rand.NewPCG(o.Seed, 0x726f61646965)) // #nosec G404 -- test data, reproducible on purpose
				for i := 1; i <= n; i++ {
					if !y(newPerson(r, i)) {
						return
					}
				}
			}
		}
		emit := func(t *core.Table, vals ...core.Value) bool { return yield(Record{Table: t, Values: vals}) }
		// actionscheduler_actions: one follow-up mail per order.
		id := 0
		for p := range people() {
			for k := range 2 {
				id++
				args := fmt.Sprintf(`{"order_id":%d,"email":"%s"}`, 2*p.id-1+k, p.email)
				if !emit(ts[0], num(id), str("woocommerce_send_follow_up"), str("pending"), str(args), null()) {
					return
				}
			}
		}
		// comments: order notes naming the customer.
		id = 0
		for p := range people() {
			for k := range 2 {
				id++
				note := fmt.Sprintf("Order status changed from Pending to Processing. Called %s %s on %s about the delivery.", p.first, p.last, p.phone)
				if !emit(ts[1], num(id), num(2*p.id-1+k), str("WooCommerce"), str("woocommerce@shop.example.com"), str(""), str(note), str("order_note")) {
					return
				}
			}
		}
		// options: the site, the admin and a live gateway.
		stripe := "a:3:{" + phpString("enabled") + phpString("yes") + phpString("secret_key") + phpString(o.LiveKey) +
			phpString("testmode") + phpString("no") + "}"
		opts := [][2]string{{"siteurl", "https://shop.example.com"}, {"home", "https://shop.example.com"},
			{"admin_email", "owner@example.com"}, {"blogdescription", "Hand-made goods from https://shop.example.com/about"},
			{"woocommerce_version", "9.8.0"}, {"woocommerce_custom_orders_table_enabled", "yes"},
			{"woocommerce_stripe_settings", stripe}}
		for i, kv := range opts {
			if !emit(ts[2], num(i+1), str(kv[0]), str(kv[1]), str("yes")) {
				return
			}
		}
		// usermeta: names, billing details and the role.
		id = 0
		for p := range people() {
			meta := [][2]string{{"first_name", p.first}, {"last_name", p.last}, {"billing_phone", p.phone},
				{"billing_email", p.email}, {"billing_address_1", p.street}, {"billing_city", p.city},
				{"wp_capabilities", `a:1:{s:8:"customer";b:1;}`}}
			if p.id == 1 {
				meta[len(meta)-1][1] = `a:1:{s:13:"administrator";b:1;}`
			}
			for _, kv := range meta {
				id++
				if !emit(ts[3], num(id), num(p.id), str(kv[0]), str(kv[1])) {
					return
				}
			}
		}
		// users.
		for p := range people() {
			if !emit(ts[4], num(p.id), str(p.login), str("$P$B"+strings.Repeat("x", 30)), str(p.login), str(p.email),
				str(""), str("2026-01-02 03:04:05"), str(""), num(0), str(p.first+" "+p.last)) {
				return
			}
		}
		// wc_order_addresses: billing and shipping per order.
		id = 0
		for p := range people() {
			for k := range 2 {
				for _, typ := range []string{"billing", "shipping"} {
					id++
					if !emit(ts[5], num(id), num(2*p.id-1+k), str(typ), str(p.first), str(p.last), str(""), str(p.street), str(""),
						str(p.city), str(""), str(p.postcode), str("GB"), str(p.email), str(p.phone)) {
						return
					}
				}
			}
		}
		// wc_orders.
		for p := range people() {
			for k := range 2 {
				oid := 2*p.id - 1 + k
				if !emit(ts[6], num(oid), str("wc-processing"), str("GBP"), str("shop_order"), str(fmt.Sprintf("%d.50000000", 10+oid%90)),
					num(p.id), str(p.email), str("2026-02-03 04:05:06"), str(p.ip), str(agents[oid%len(agents)]),
					str("Please leave it with "+p.first+" next door"), str("ch_3Q"+strconv.Itoa(oid))) {
					return
				}
			}
		}
		// wc_orders_meta: the billing details and the search index.
		id = 0
		for p := range people() {
			for k := range 2 {
				oid := 2*p.id - 1 + k
				index := strings.Join([]string{p.first, p.last, p.street, p.city, p.postcode, "GB", p.email, p.phone}, " ")
				for _, kv := range [][2]string{{"_billing_first_name", p.first}, {"_billing_last_name", p.last},
					{"_billing_email", p.email}, {"_billing_phone", p.phone}, {"_billing_address_index", index}} {
					id++
					if !emit(ts[7], num(id), num(oid), str(kv[0]), str(kv[1])) {
						return
					}
				}
			}
		}
	}
}

// escape writes s as a MySQL string literal body.
func escape(w *bufio.Writer, s []byte) {
	for _, c := range s {
		switch c {
		case 0:
			_, _ = w.WriteString(`\0`)
		case '\n':
			_, _ = w.WriteString(`\n`)
		case '\r':
			_, _ = w.WriteString(`\r`)
		case '\\':
			_, _ = w.WriteString(`\\`)
		case '\'':
			_, _ = w.WriteString(`\'`)
		case '"':
			_, _ = w.WriteString(`\"`)
		case 0x1a:
			_, _ = w.WriteString(`\Z`)
		default:
			_ = w.WriteByte(c)
		}
	}
}

// createTable is the table's CREATE TABLE statement.
func createTable(t *core.Table) string {
	var b strings.Builder
	fmt.Fprintf(&b, "DROP TABLE IF EXISTS `%s`;\nCREATE TABLE `%s` (\n", t.Name, t.Name)
	for _, c := range t.Columns {
		null := " NOT NULL"
		if c.Nullable {
			null = " DEFAULT NULL"
		}
		fmt.Fprintf(&b, "  `%s` %s%s,\n", c.Name, c.Type, null)
	}
	fmt.Fprintf(&b, "  PRIMARY KEY (`%s`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;\n", t.PrimaryKey[0])
	return b.String()
}

// insertBytes is about how long one INSERT gets, as mysqldump's
// extended inserts.
const insertBytes = 1 << 20

// Write writes the dump as mysqldump would and returns its size.
func Write(w io.Writer, o Options) (int64, error) {
	cw := &countWriter{w: w}
	bw := bufio.NewWriterSize(cw, 1<<16)
	_, _ = bw.WriteString("-- MySQL dump 10.13  Distrib 8.0.40, for Linux (x86_64)\n--\n-- Host: localhost    Database: shop\n" +
		"/*!40101 SET NAMES utf8mb4 */;\n/*!40014 SET @OLD_FOREIGN_KEY_CHECKS=@@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS=0 */;\n")
	var cur *core.Table
	stmt := 0
	end := func() {
		if stmt > 0 {
			_, _ = bw.WriteString(";\n")
		}
		if cur != nil {
			_, _ = bw.WriteString("UNLOCK TABLES;\n")
		}
		stmt = 0
	}
	for r := range Records(o) {
		if r.Table != cur {
			end()
			cur = r.Table
			_, _ = bw.WriteString("\n" + createTable(cur) + "LOCK TABLES `" + cur.Name + "` WRITE;\n")
		}
		if stmt == 0 {
			_, _ = bw.WriteString("INSERT INTO `" + cur.Name + "` VALUES ")
		} else {
			_ = bw.WriteByte(',')
		}
		before := cw.n + int64(bw.Buffered())
		_ = bw.WriteByte('(')
		for i, v := range r.Values {
			if i > 0 {
				_ = bw.WriteByte(',')
			}
			switch v.Kind {
			case core.Null:
				_, _ = bw.WriteString("NULL")
			case core.String:
				_ = bw.WriteByte('\'')
				escape(bw, v.Raw)
				_ = bw.WriteByte('\'')
			default:
				_, _ = bw.Write(v.Raw)
			}
		}
		_ = bw.WriteByte(')')
		stmt += int(cw.n + int64(bw.Buffered()) - before)
		if stmt >= insertBytes {
			_, _ = bw.WriteString(";\n")
			stmt = 0
		}
	}
	end()
	_, _ = bw.WriteString("/*!40014 SET FOREIGN_KEY_CHECKS=@OLD_FOREIGN_KEY_CHECKS */;\n\n-- Dump completed on 2026-10-26  0:00:00\n")
	if err := bw.Flush(); err != nil {
		return cw.n, err
	}
	return cw.n, cw.err
}

// countWriter counts what passes through.
type countWriter struct {
	w   io.Writer
	n   int64
	err error
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if err != nil && c.err == nil {
		c.err = err
	}
	return n, err
}

// Source streams the same rows as batches without SQL in between, for
// benchmarks of the engine alone: one CREATE TABLE statement per table,
// then batches of at most core.MaxBatchBytes. Offset counts the bytes of
// the values as the dump would hold them.
func Source(o Options) core.Source {
	next, stop := iter.Pull(Records(o))
	return &source{o: o, next: next, stop: stop, schema: &core.Schema{Dialect: core.MySQL, Prefix: o.prefix()}}
}

type source struct {
	o       Options
	next    func() (Record, bool)
	stop    func()
	schema  *core.Schema
	pending *Record
	cur     *core.Table
	seq     uint64
	off     int64
	done    bool
}

func (s *source) Schema() *core.Schema { return s.schema }

func (s *source) Next(ctx context.Context) (*core.Batch, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.pending == nil && !s.done {
		r, ok := s.next()
		if !ok {
			s.done = true
		} else {
			s.pending = &r
		}
	}
	if s.pending == nil {
		return nil, io.EOF
	}
	if s.pending.Table != s.cur {
		s.cur = s.pending.Table
		s.schema.Tables = append(s.schema.Tables, s.cur)
		text := createTable(s.cur)
		s.off += int64(len(text))
		s.seq++
		return &core.Batch{Seq: s.seq, Pass: &core.Statement{Kind: core.StmtCreateTable, Text: []byte(text)}, Offset: s.off}, nil
	}
	b := &core.Batch{Table: s.cur, First: true, Last: true,
		Head: []byte("INSERT INTO `" + s.cur.Name + "` VALUES "), Tail: []byte(";\n")}
	for i := range s.cur.Columns {
		b.Columns = append(b.Columns, i)
	}
	size := 0
	for s.pending != nil && s.pending.Table == s.cur && size < core.MaxBatchBytes {
		row := core.Row{Values: s.pending.Values}
		if len(b.Rows) > 0 {
			row.Lead = []byte(",")
		}
		for _, v := range row.Values {
			size += len(v.Raw) + 3
		}
		b.Rows = append(b.Rows, row)
		r, ok := s.next()
		if !ok {
			s.pending, s.done = nil, true
		} else {
			s.pending = &r
		}
	}
	s.off += int64(size)
	s.seq++
	b.Seq, b.Offset = s.seq, s.off
	return b, nil
}

func (s *source) Fingerprint() core.Fingerprint {
	return core.Fingerprint{Kind: "generated", Value: fmt.Sprintf("gen:%d:%d", s.o.Seed, s.o.Size), Bytes: s.off}
}

func (s *source) Close() error { s.stop(); return nil }

// Bytes returns the dump in memory, for small sizes.
func Bytes(o Options) []byte {
	var b bytes.Buffer
	_, _ = Write(&b, o)
	return b.Bytes()
}
