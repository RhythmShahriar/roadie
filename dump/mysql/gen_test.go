// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package mysql

import (
	"bufio"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"
)

// genDump writes a synthetic WooCommerce-shaped mysqldump of about size
// bytes: wp_posts, wp_postmeta (30% of the rows, with PHP serialized
// values), wp_users and wp_comments, in extended INSERTs of about 1 MB
// (mysqldump's default net_buffer_length). The shape follows the 378 MB
// production dump the benchmark targets were measured on; every value is
// made up and every address uses a reserved domain.
func genDump(w io.Writer, size int64, seed uint64) int64 {
	bw := bufio.NewWriterSize(w, 1<<20)
	defer bw.Flush()
	r := rand.New(rand.NewPCG(seed, 7))
	var n int64
	put := func(s string) { n += int64(len(s)); bw.WriteString(s) }
	put("-- MySQL dump 10.13  Distrib 8.0.36, for Linux (x86_64)\n--\n-- Host: localhost    Database: shop\n-- Server version\t8.0.36\n\n")
	put("/*!40101 SET NAMES utf8mb4 */;\n/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;\n")
	tables := []struct {
		name, create string
		weight       int
		row          func(id int) string
	}{
		{"wp_posts", "CREATE TABLE `wp_posts` (\n  `ID` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `post_author` bigint unsigned NOT NULL DEFAULT '0',\n  `post_date` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',\n  `post_content` longtext NOT NULL,\n  `post_title` text NOT NULL,\n  `post_status` varchar(20) NOT NULL DEFAULT 'publish',\n  `post_type` varchar(20) NOT NULL DEFAULT 'post',\n  PRIMARY KEY (`ID`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;\n", 40,
			func(id int) string {
				return fmt.Sprintf("(%d,1,'2024-05-%02d 10:%02d:00','<p>Order notes for order %d.\\r\\nThe customer asked: \\\"Can it ship on Monday?\\\" Lorem ipsum dolor sit amet, consectetur adipiscing elit.</p>','Order &ndash; %d','wc-completed','shop_order')",
					id, 1+r.IntN(28), r.IntN(60), id, id)
			}},
		{"wp_postmeta", "CREATE TABLE `wp_postmeta` (\n  `meta_id` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `post_id` bigint unsigned NOT NULL DEFAULT '0',\n  `meta_key` varchar(255) DEFAULT NULL,\n  `meta_value` longtext,\n  PRIMARY KEY (`meta_id`),\n  KEY `post_id` (`post_id`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;\n", 30,
			func(id int) string {
				switch id % 4 {
				case 0:
					return fmt.Sprintf("(%d,%d,'_billing_email','customer%d@example.test')", id, id/4, id)
				case 1:
					return fmt.Sprintf("(%d,%d,'_billing_address_1','%d Example Street')", id, id/4, r.IntN(999))
				case 2:
					return fmt.Sprintf("(%d,%d,'_customer_ip_address','192.0.2.%d')", id, id/4, r.IntN(255))
				}
				return fmt.Sprintf("(%d,%d,'_shipping_lines','a:2:{s:4:\\\"name\\\";s:13:\\\"Flat rate %03d\\\";s:5:\\\"total\\\";s:4:\\\"4.99\\\";}')", id, id/4, r.IntN(1000))
			}},
		{"wp_users", "CREATE TABLE `wp_users` (\n  `ID` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `user_login` varchar(60) NOT NULL DEFAULT '',\n  `user_email` varchar(100) NOT NULL DEFAULT '',\n  `display_name` varchar(250) NOT NULL DEFAULT '',\n  PRIMARY KEY (`ID`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;\n", 10,
			func(id int) string {
				return fmt.Sprintf("(%d,'user%d','user%d@example.test','Test User %d')", id, id, id, id)
			}},
		{"wp_comments", "CREATE TABLE `wp_comments` (\n  `comment_ID` bigint unsigned NOT NULL AUTO_INCREMENT,\n  `comment_author_email` varchar(100) NOT NULL DEFAULT '',\n  `comment_author_IP` varchar(100) NOT NULL DEFAULT '',\n  `comment_content` text NOT NULL,\n  `comment_type` varchar(20) NOT NULL DEFAULT 'order_note',\n  PRIMARY KEY (`comment_ID`)\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci;\n", 20,
			func(id int) string {
				return fmt.Sprintf("(%d,'note%d@example.test','198.51.100.%d','Order status changed from Pending payment to Processing.','order_note')", id, id, r.IntN(255))
			}},
	}
	for _, t := range tables {
		budget := size * int64(t.weight) / 100
		put("\n--\n-- Table structure for table `" + t.name + "`\n--\n\nDROP TABLE IF EXISTS `" + t.name + "`;\n")
		put(t.create)
		put("LOCK TABLES `" + t.name + "` WRITE;\n/*!40000 ALTER TABLE `" + t.name + "` DISABLE KEYS */;\n")
		id, start := 1, n
		for n-start < budget {
			put("INSERT INTO `" + t.name + "` VALUES ")
			stmt := n
			for first := true; n-stmt < 1<<20 && n-start < budget; first = false {
				if !first {
					put(",")
				}
				put(t.row(id))
				id++
			}
			put(";\n")
		}
		put("/*!40000 ALTER TABLE `" + t.name + "` ENABLE KEYS */;\nUNLOCK TABLES;\n")
	}
	put("/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;\n\n-- Dump completed on 2026-10-26 10:00:00\n")
	return n
}

// benchSize is the generated dump size for benchmarks: 32 MB by default,
// so the CI smoke run (-benchtime 1x) stays quick, and the 378 MB shape
// with ROADIE_BENCH_FULL=1 for the recorded numbers.
func benchSize() int64 {
	if os.Getenv("ROADIE_BENCH_FULL") == "1" {
		return 378_000_000
	}
	if s := os.Getenv("ROADIE_BENCH_MB"); s != "" {
		if mb, err := strconv.Atoi(s); err == nil {
			return int64(mb) * 1_000_000
		}
	}
	return 32_000_000
}

// benchDump writes the benchmark dump once per process into a temp file.
func benchDump(b *testing.B) string {
	b.Helper()
	path := b.TempDir() + "/bench.sql"
	f, err := os.Create(path)
	if err != nil {
		b.Fatal(err)
	}
	genDump(f, benchSize(), 1)
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}
	return path
}

func TestGenDump(t *testing.T) {
	var buf countWriter
	n := genDump(&buf, 3_000_000, 1)
	if n != int64(buf) || n < 3_000_000 {
		t.Fatalf("wrote %d, counted %d", buf, n)
	}
}

type countWriter int64

func (c *countWriter) Write(p []byte) (int, error) { *c += countWriter(len(p)); return len(p), nil }
