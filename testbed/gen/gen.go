// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package gen writes r17's large dump: a WooCommerce-shaped mysqldump of a
// requested size (200 MB, 2 GB, 20 GB in the nightly) for the testbed's
// throughput and memory runs (PRD 22.3, qa.md 6.2 r17).
//
// The dump is what makes a streaming pipeline sweat: one 50 MB row, a
// --hex-blob table, a trigger in a DELIMITER block, realistic serialized
// usermeta and postmeta; canaries sit in the very first row, around the
// 4 GiB offset (where 32-bit offsets wrap) and in the very last row. The
// same seed and size give the same bytes. Write streams; it never holds
// more than one INSERT statement (or the big row) in memory.
package gen

import (
	"bufio"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"

	"github.com/RhythmShahriar/roadie/testbed/oracle"
	"github.com/RhythmShahriar/roadie/testbed/seed"
)

// Scenario is the fixture whose canaries the generator makes.
const Scenario = "r17"

// BigRow is the size of the one huge value (a cached report in
// wp_options), capped at a quarter of a small dump.
const BigRow = 50 << 20

// FourGiB is the offset canaries sit around: a reader that keeps offsets
// in 32 bits wraps here.
const FourGiB = int64(4) << 30

// statementSize is where an extended INSERT ends, as mysqldump's
// net_buffer_length does.
const statementSize = 1 << 20

// Options says what to write.
type Options struct {
	Size int64 // bytes of SQL, before compression
	Seed int64
	// Values makes the canaries (seed.NewValues(seed, "r17")); sharing
	// one across files keeps every canary ID unique.
	Values *seed.Values
	// SerializedEvery lists every Nth serialized cell for the integrity
	// sample (default 1,000).
	SerializedEvery int
	// Around is the offset two canaries straddle (default FourGiB); a
	// test moves it to see the placement without writing 4 GiB.
	Around int64
}

// Placed is a canary and the byte offset of its value in the SQL.
type Placed struct {
	ID     string `json:"id"`
	Offset int64  `json:"offset"`
	Where  string `json:"where"` // first-row, around-4gib, last-row
}

// Result is what Write wrote.
type Result struct {
	Bytes      int64             `json:"bytes"`
	Rows       map[string]int64  `json:"rows"`
	Canaries   []seed.Canary     `json:"-"`
	Placed     []Placed          `json:"placed"`
	BigRow     Span              `json:"big_row"`
	Serialized []oracle.Location `json:"-"`
}

// Span is where a value sits in the SQL.
type Span struct {
	Table  string `json:"table"`
	Offset int64  `json:"offset"`
	Length int64  `json:"length"`
}

// counter counts the bytes written through it.
type counter struct {
	w io.Writer
	n int64
}

func (c *counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// writer is one dump in progress.
type writer struct {
	o    Options
	c    *counter
	bw   *bufio.Writer
	r    *rand.Rand
	res  *Result
	stmt []byte // the INSERT being built
	// pending4G are the canaries still to place around 4 GiB.
	pending4G int
	serial    int
	scratch   []byte
}

// off is the offset the next byte will have.
func (w *writer) off() int64 { return w.c.n + int64(w.bw.Buffered()) }

func (w *writer) str(s string) { _, _ = w.bw.WriteString(s) }

// Write writes the dump to out.
func Write(out io.Writer, o Options) (*Result, error) {
	if o.Values == nil {
		return nil, errors.New("gen: Options.Values is nil")
	}
	if o.SerializedEvery <= 0 {
		o.SerializedEvery = 1000
	}
	c := &counter{w: out}
	w := &writer{o: o, c: c, bw: bufio.NewWriterSize(c, 4<<20), r: rand.New(rand.NewPCG(uint64(o.Seed), 17)), // #nosec G115 G404 -- test data from the seed
		res: &Result{Rows: map[string]int64{}}}
	if o.Around <= 0 {
		o.Around = FourGiB
	}
	w.o = o
	if o.Size > o.Around {
		w.pending4G = 2
	}
	if err := w.run(); err != nil {
		return nil, err
	}
	if err := w.bw.Flush(); err != nil {
		return nil, err
	}
	w.res.Bytes = c.n
	return w.res, nil
}

const head = `-- MySQL dump 10.13  Distrib 8.4.3, for Linux (x86_64)
--
-- Host: localhost    Database: shop
-- ------------------------------------------------------
-- Server version	8.4.3

/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;
/*!50503 SET NAMES utf8mb4 */;
/*!40103 SET @OLD_TIME_ZONE=@@TIME_ZONE */;
/*!40103 SET TIME_ZONE='+00:00' */;
/*!40014 SET @OLD_UNIQUE_CHECKS=@@UNIQUE_CHECKS, UNIQUE_CHECKS=0 */;
/*!40014 SET @OLD_FOREIGN_KEY_CHECKS=@@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS=0 */;
/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;
`

const tail = `/*!40103 SET TIME_ZONE=@OLD_TIME_ZONE */;
/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;
/*!40014 SET FOREIGN_KEY_CHECKS=@OLD_FOREIGN_KEY_CHECKS */;
/*!40014 SET UNIQUE_CHECKS=@OLD_UNIQUE_CHECKS */;
/*!40101 SET CHARACTER_SET_CLIENT=@OLD_CHARACTER_SET_CLIENT */;

-- Dump completed on 2026-10-01 12:00:00
`

// trigger is what mysqldump writes after wp_users' data when the table
// has a trigger: a DELIMITER block with a versioned CREATE TRIGGER.
const trigger = `/*!50003 SET @saved_cs_client      = @@character_set_client */ ;
/*!50003 SET character_set_client  = utf8mb4 */ ;
DELIMITER ;;
/*!50003 CREATE*/ /*!50017 DEFINER=` + "`shop`@`localhost`" + `*/ /*!50003 TRIGGER ` + "`wp_users_lower_email`" + ` BEFORE INSERT ON ` + "`wp_users`" + ` FOR EACH ROW BEGIN
  SET NEW.user_email = LOWER(NEW.user_email);
END */;;
DELIMITER ;
/*!50003 SET character_set_client  = @saved_cs_client */ ;
`

// tables, in the order they are written, with their share of the bulk.
var tables = []struct {
	name   string
	create string
	share  float64
}{
	{"wp_users", "CREATE TABLE `wp_users` (\n  `ID` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `user_login` varchar(60) NOT NULL DEFAULT '',\n  `user_pass` varchar(255) NOT NULL DEFAULT '',\n  `user_nicename` varchar(50) NOT NULL DEFAULT '',\n  `user_email` varchar(100) NOT NULL DEFAULT '',\n  `user_url` varchar(100) NOT NULL DEFAULT '',\n  `user_registered` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',\n  `user_activation_key` varchar(255) NOT NULL DEFAULT '',\n  `user_status` int NOT NULL DEFAULT '0',\n  `display_name` varchar(250) NOT NULL DEFAULT '',\n  PRIMARY KEY (`ID`),\n  KEY `user_email` (`user_email`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;\n", 0.25},
	{"wp_options", "CREATE TABLE `wp_options` (\n  `option_id` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `option_name` varchar(191) NOT NULL DEFAULT '',\n  `option_value` longtext NOT NULL,\n  `autoload` varchar(20) NOT NULL DEFAULT 'yes',\n  PRIMARY KEY (`option_id`),\n  UNIQUE KEY `option_name` (`option_name`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;\n", 0},
	{"wp_testbed_blobs", "CREATE TABLE `wp_testbed_blobs` (\n  `id` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `name` varchar(191) NOT NULL DEFAULT '',\n  `data` longblob,\n  PRIMARY KEY (`id`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;\n", 0.05},
	{"wp_usermeta", "CREATE TABLE `wp_usermeta` (\n  `umeta_id` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `user_id` bigint unsigned NOT NULL DEFAULT '0',\n  `meta_key` varchar(255) DEFAULT NULL,\n  `meta_value` longtext,\n  PRIMARY KEY (`umeta_id`),\n  KEY `user_id` (`user_id`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;\n", 0.45},
	{"wp_postmeta", "CREATE TABLE `wp_postmeta` (\n  `meta_id` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `post_id` bigint unsigned NOT NULL DEFAULT '0',\n  `meta_key` varchar(255) DEFAULT NULL,\n  `meta_value` longtext,\n  PRIMARY KEY (`meta_id`),\n  KEY `post_id` (`post_id`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;\n", 0.25},
}

func (w *writer) run() error {
	w.str(head)
	big := min(int64(BigRow), w.o.Size/4)
	fixed := int64(len(head) + len(tail) + len(trigger) + 4096)
	for _, t := range tables {
		fixed += int64(len(t.create)) + 200
	}
	bulk := w.o.Size - fixed - big
	if bulk < 64<<10 {
		return fmt.Errorf("gen: %d bytes is too small (at least %d)", w.o.Size, fixed+big+64<<10)
	}
	start := w.off()
	for i, t := range tables {
		w.str("\n--\n-- Table structure for table `" + t.name + "`\n--\n\nDROP TABLE IF EXISTS `" + t.name + "`;\n")
		w.str(t.create)
		w.str("\n--\n-- Dumping data for table `" + t.name + "`\n--\n\nLOCK TABLES `" + t.name + "` WRITE;\n")
		// Each bulk table fills to where its share ends, so the total lands
		// on Size whatever the earlier tables overshot.
		var until float64
		for _, u := range tables[:i+1] {
			until += u.share
		}
		end := start + int64(until*float64(bulk))
		if t.name == "wp_options" {
			end = 0
		}
		if i == len(tables)-1 {
			end = w.o.Size - int64(len(tail)) - 64
		}
		if err := w.table(t.name, end, big, i == len(tables)-1); err != nil {
			return err
		}
		w.str("UNLOCK TABLES;\n")
		if t.name == "wp_users" {
			w.str(trigger)
		}
		if t.name == "wp_options" {
			start += w.res.BigRow.Length // the big row isn't part of the bulk budget
		}
	}
	w.str("\n" + tail)
	return nil
}

// table writes rows of name until the offset reaches end; the last
// table ends with the last-row canary.
func (w *writer) table(name string, end, big int64, last bool) error {
	id := int64(0)
	open := false
	flush := func() {
		if open {
			w.stmt = append(w.stmt, ";\n"...)
			_, _ = w.bw.Write(w.stmt)
			w.stmt = w.stmt[:0]
			open = false
		}
	}
	add := func(row string, canaries ...canaryAt) {
		if open && len(w.stmt)+len(row)+1 > statementSize {
			flush()
		}
		if !open {
			w.stmt = append(w.stmt, "INSERT INTO `"+name+"` VALUES "...)
			open = true
		} else {
			w.stmt = append(w.stmt, ',')
		}
		at := w.off() + int64(len(w.stmt))
		for _, c := range canaries {
			if i := strings.Index(row, c.value); i >= 0 {
				w.res.Placed = append(w.res.Placed, Placed{ID: c.id, Offset: at + int64(i), Where: c.where})
			}
		}
		w.stmt = append(w.stmt, row...)
		w.res.Rows[name]++
	}
	if name == "wp_options" {
		add("(1,'siteurl','https://shop.example.com','on')")
		add("(2,'home','https://shop.example.com','on')")
		flush()
		// The big row: a serialized cached report, alone in its statement.
		const line = "Bean & Leaf order export line, nothing personal here. "
		body := strings.Repeat(line, int(big)/len(line)+1)[:big]
		val := fmt.Sprintf(`a:1:{s:6:\"report\";s:%d:\"%s\";}`, len(body), body)
		prefix := "INSERT INTO `wp_options` VALUES (3,'_transient_testbed_big_report','"
		w.res.BigRow = Span{Table: name, Offset: w.off() + int64(len(prefix)), Length: int64(len(val))}
		w.str(prefix + val + "','off');\n")
		w.res.Rows[name]++
		return nil
	}
	for {
		id++
		where := ""
		switch {
		case id == 1 && name == "wp_users":
			where = "first-row"
		case w.pending4G > 0 && w.off()+int64(len(w.stmt)) > w.next4G() && name != "wp_testbed_blobs":
			where = "around-4gib"
			w.pending4G--
		}
		row, cs, err := w.row(name, id, where)
		if err != nil {
			return err
		}
		add(row, cs...)
		if w.off()+int64(len(w.stmt)) >= end {
			break
		}
	}
	if last {
		row, cs, err := w.row(name, id+1, "last-row")
		if err != nil {
			return err
		}
		add(row, cs...)
	}
	flush()
	return nil
}

// next4G is where the next canary around 4 GiB (Options.Around) goes:
// 256 KiB before the boundary, then 256 KiB after it. The boundary falls
// in wp_users in every size that reaches it (20 GB: wp_users holds the
// first quarter); were it inside the big row or the blob table, both
// canaries would land just after.
func (w *writer) next4G() int64 {
	if w.pending4G == 2 {
		return w.o.Around - 256<<10
	}
	return w.o.Around + 256<<10
}

// passHash is a password-hash-shaped value for a login (not a real hash
// of anything).
func passHash(login string) string {
	h := hex.EncodeToString([]byte(login + "testbedsalt0000000000"))
	return h[:40]
}

// canaryAt is a canary value a row holds.
type canaryAt struct{ id, value, where string }

var (
	firstNames = []string{"Olivia", "Liam", "Amelia", "Noah", "Isla", "Oliver", "Ava", "Elijah", "Mia", "James", "Sofia", "Lucas", "Freya", "Mateo", "Ella", "Arjun", "Zara", "Kai", "Nora", "Felix"}
	lastNames  = []string{"Hughes", "Novak", "Okafor", "Lindqvist", "Moreau", "Brennan", "Kowalski", "Haddad", "Castillo", "Fischer", "Murphy", "Tanaka", "Silva", "Petrov", "Rossi", "Khan", "Walsh", "Dubois", "Jensen", "Ortega"}
	domains    = []string{"gmail.com", "outlook.com", "hotmail.co.uk", "yahoo.com", "icloud.com", "proton.me"}
)

// person is a real-looking (invented) customer.
func (w *writer) person() (first, last, email, phone, ip string) {
	first = firstNames[w.r.IntN(len(firstNames))]
	last = lastNames[w.r.IntN(len(lastNames))]
	email = lowerNames[first] + "." + lowerNames[last] + strconv.Itoa(1000+w.r.IntN(9000)) + "@" + domains[w.r.IntN(len(domains))]
	phone = "+44 7911 " + strconv.Itoa(100000+w.r.IntN(900000))
	b := strconv.AppendInt(make([]byte, 0, 16), int64(81+w.r.IntN(100)), 10)
	for _, n := range []int{w.r.IntN(256), w.r.IntN(256), 1 + w.r.IntN(254)} {
		b = strconv.AppendInt(append(b, '.'), int64(n), 10)
	}
	return first, last, email, phone, string(b)
}

// lowerNames are the names in lower case, made once.
var lowerNames = func() map[string]string {
	m := map[string]string{}
	for _, n := range append(slices.Clone(firstNames), lastNames...) {
		m[n] = strings.ToLower(n)
	}
	return m
}()

// rowBuf builds one row without fmt: the generator's hot path.
type rowBuf []byte

func (b rowBuf) int(n int64) rowBuf  { return strconv.AppendInt(b, n, 10) }
func (b rowBuf) str(s string) rowBuf { return append(b, s...) }

// ser appends a PHP serialized string, its quotes escaped for SQL.
func (b rowBuf) ser(s string) rowBuf {
	b = strconv.AppendInt(append(b, "s:"...), int64(len(s)), 10)
	return append(append(append(b, `:\"`...), s...), `\";`...)
}

const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15"

// row returns one row of table with id; where places canaries in it.
func (w *writer) row(table string, id int64, where string) (string, []canaryAt, error) {
	first, last, email, phone, ip := w.person()
	var cs []canaryAt
	canary := func(kind, column string) (string, error) {
		if where == "" {
			return "", nil
		}
		c, err := w.o.Values.Canary(kind, table, column)
		if err != nil {
			return "", err
		}
		w.res.Canaries = append(w.res.Canaries, c)
		cs = append(cs, canaryAt{c.ID, c.Value, where})
		return c.Value, nil
	}
	b := rowBuf(w.scratch[:0])
	defer func() { w.scratch = b[:0] }()
	switch table {
	case "wp_users":
		if v, err := canary("email", "user_email"); err != nil {
			return "", nil, err
		} else if v != "" {
			email = v
		}
		name := first + " " + last
		if v, err := canary("full_name", "display_name"); err != nil {
			return "", nil, err
		} else if v != "" {
			name = v
		}
		login := lowerNames[first] + strconv.FormatInt(id, 10)
		b = b.str("(").int(id).str(",'").str(login).str("','$wp$2y$10$").str(passHash(login)).str("','").str(login).str("','").str(email).
			str("','','2026-0").int(int64(1 + w.r.IntN(9))).str("-1").int(int64(w.r.IntN(10))).str(" 10:00:00','',0,'").str(name).str("')")
		return string(b), cs, nil
	case "wp_testbed_blobs":
		n := 512 + w.r.IntN(1536)
		raw := make([]byte, n+8)
		for i := 0; i < n; i += 8 {
			binary.LittleEndian.PutUint64(raw[i:], w.r.Uint64())
		}
		b = b.str("(").int(id).str(",'thumb-").int(id).str(".webp',0x")
		b = hex.AppendEncode(b, raw[:n])
		return string(append(b, ')')), nil, nil
	case "wp_usermeta":
		user := (id+3)/4 + 1
		kind := id % 4
		if where != "" {
			kind = 0 // a canary row is a billing email
		}
		b = b.str("(").int(id).str(",").int(user)
		switch kind {
		case 0:
			e, err := canary("email", "meta_value")
			if err != nil {
				return "", nil, err
			}
			if e != "" {
				email = e
			}
			b = b.str(",'billing_email','").str(email).str("')")
			return string(b), cs, nil
		case 1:
			b = b.str(",'billing_phone','").str(phone).str("')")
		case 2:
			tok := hex.EncodeToString(strconv.AppendInt(make([]byte, 0, 32), 1e15+id, 10))
			b = b.str(",'session_tokens','a:1:{").ser(tok).str("a:4:{").ser("expiration").str("i:1790000000;").
				ser("ip").ser(ip).ser("ua").ser(userAgent).ser("login").str("i:1780000000;}}')")
			w.serialCell("wp_usermeta", "umeta_id", id)
		default:
			b = b.str(",'wp_capabilities','a:1:{").ser("customer").str("b:1;}')")
			w.serialCell("wp_usermeta", "umeta_id", id)
		}
		return string(b), nil, nil
	case "wp_postmeta":
		e, err := canary("email", "meta_value")
		if err != nil {
			return "", nil, err
		}
		if e != "" {
			email = e
		}
		p, err := canary("phone", "meta_value")
		if err != nil {
			return "", nil, err
		}
		if p != "" {
			phone = p
		}
		w.serialCell("wp_postmeta", "meta_id", id)
		b = b.str("(").int(id).str(",").int(1000 + id/3).str(",'_testbed_order_contact','a:4:{").ser("email").ser(email).
			ser("phone").ser(phone).ser("name").ser(first + " " + last).ser("ip").ser(ip).str("}')")
		return string(b), cs, nil
	}
	return "", nil, fmt.Errorf("gen: no rows for %s", table)
}

// serialCell lists every SerializedEvery-th serialized cell for the
// integrity sample.
func (w *writer) serialCell(table, key string, id int64) {
	w.serial++
	if w.serial%w.o.SerializedEvery == 1 || w.o.SerializedEvery == 1 {
		col := "meta_value"
		w.res.Serialized = append(w.res.Serialized, oracle.Location{Table: table, Key: key + "=" + strconv.FormatInt(id, 10), Column: col})
	}
}

// ParseSize reads "200MB", "2GB", "20GB", "1.5GiB" or a byte count. KB, MB
// and GB are powers of 1,000; KiB, MiB and GiB powers of 1,024.
func ParseSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	mult := map[string]float64{"KIB": 1 << 10, "MIB": 1 << 20, "GIB": 1 << 30, "KB": 1e3, "MB": 1e6, "GB": 1e9, "B": 1}
	up := strings.ToUpper(s)
	for _, suf := range []string{"KIB", "MIB", "GIB", "KB", "MB", "GB", "B"} {
		if num, ok := strings.CutSuffix(up, suf); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(num), 64)
			n := f * mult[suf]
			if err != nil || !(n >= 1) || n > 1<<62 {
				return 0, fmt.Errorf("size %q: want a number and KB, MB, GB, KiB, MiB or GiB", s)
			}
			return int64(n), nil
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("size %q: want a number and KB, MB, GB, KiB, MiB or GiB", s)
	}
	return n, nil
}
