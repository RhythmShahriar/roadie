// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package demo

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// table is one table of the dump: its CREATE TABLE body and its rows.
// A cell is an int, a string or nil (NULL).
type table struct {
	name   string
	create string // the lines between "CREATE TABLE `name` (" and ") ENGINE"
	engine string // the table options after the closing parenthesis
	rows   [][]any
}

// insertLimit is where an extended INSERT is split, as mysqldump splits at
// its net_buffer_length (16 KiB by default here, so the demo dump has
// several INSERT statements for its larger tables, as a real one does).
const insertLimit = 16 << 10

// dumpHeader and dumpFooter are what mysqldump 8.4 writes around the tables.
const dumpHeader = `-- MySQL dump 10.13  Distrib 8.4.3, for Linux (x86_64)
--
-- Host: localhost    Database: shop
-- ------------------------------------------------------
-- Server version	8.4.3

/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;
/*!40101 SET @OLD_CHARACTER_SET_RESULTS=@@CHARACTER_SET_RESULTS */;
/*!40101 SET @OLD_COLLATION_CONNECTION=@@COLLATION_CONNECTION */;
/*!50503 SET NAMES utf8mb4 */;
/*!40103 SET @OLD_TIME_ZONE=@@TIME_ZONE */;
/*!40103 SET TIME_ZONE='+00:00' */;
/*!40014 SET @OLD_UNIQUE_CHECKS=@@UNIQUE_CHECKS, UNIQUE_CHECKS=0 */;
/*!40014 SET @OLD_FOREIGN_KEY_CHECKS=@@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS=0 */;
/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;
/*!40111 SET @OLD_SQL_NOTES=@@SQL_NOTES, SQL_NOTES=0 */;
`

const dumpFooter = `/*!40103 SET TIME_ZONE=@OLD_TIME_ZONE */;

/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;
/*!40014 SET FOREIGN_KEY_CHECKS=@OLD_FOREIGN_KEY_CHECKS */;
/*!40014 SET UNIQUE_CHECKS=@OLD_UNIQUE_CHECKS */;
/*!40101 SET CHARACTER_SET_CLIENT=@OLD_CHARACTER_SET_CLIENT */;
/*!40101 SET CHARACTER_SET_RESULTS=@OLD_CHARACTER_SET_RESULTS */;
/*!40101 SET COLLATION_CONNECTION=@OLD_COLLATION_CONNECTION */;
/*!40111 SET SQL_NOTES=@OLD_SQL_NOTES */;

-- Dump completed on 2026-10-01 12:00:00
`

// writeTable writes t the way mysqldump does: DROP, CREATE, then the rows
// in extended INSERT statements between LOCK and UNLOCK.
func writeTable(w *bufio.Writer, t *table) {
	fmt.Fprintf(w, "\n--\n-- Table structure for table `%s`\n--\n\n", t.name)
	fmt.Fprintf(w, "DROP TABLE IF EXISTS `%s`;\n", t.name)
	fmt.Fprint(w, "/*!40101 SET @saved_cs_client     = @@character_set_client */;\n")
	fmt.Fprint(w, "/*!50503 SET character_set_client = utf8mb4 */;\n")
	fmt.Fprintf(w, "CREATE TABLE `%s` (\n%s\n) %s;\n", t.name, strings.TrimRight(t.create, "\n"), t.engine)
	fmt.Fprint(w, "/*!40101 SET character_set_client = @saved_cs_client */;\n")
	fmt.Fprintf(w, "\n--\n-- Dumping data for table `%s`\n--\n\n", t.name)
	fmt.Fprintf(w, "LOCK TABLES `%s` WRITE;\n", t.name)
	fmt.Fprintf(w, "/*!40000 ALTER TABLE `%s` DISABLE KEYS */;\n", t.name)
	var stmt strings.Builder
	flush := func() {
		if stmt.Len() > 0 {
			fmt.Fprint(w, stmt.String())
			fmt.Fprint(w, ";\n")
			stmt.Reset()
		}
	}
	for _, row := range t.rows {
		tuple := encodeRow(row)
		if stmt.Len() > 0 && stmt.Len()+len(tuple) > insertLimit {
			flush()
		}
		if stmt.Len() == 0 {
			fmt.Fprintf(&stmt, "INSERT INTO `%s` VALUES ", t.name)
		} else {
			stmt.WriteByte(',')
		}
		stmt.WriteString(tuple)
	}
	flush()
	fmt.Fprintf(w, "/*!40000 ALTER TABLE `%s` ENABLE KEYS */;\n", t.name)
	fmt.Fprint(w, "UNLOCK TABLES;\n")
}

// encodeRow renders one row as a VALUES tuple.
func encodeRow(row []any) string {
	var b strings.Builder
	b.WriteByte('(')
	for i, v := range row {
		if i > 0 {
			b.WriteByte(',')
		}
		switch v := v.(type) {
		case nil:
			b.WriteString("NULL")
		case int:
			b.WriteString(strconv.Itoa(v))
		case string:
			b.WriteByte('\'')
			b.WriteString(sqlEscape(v))
			b.WriteByte('\'')
		default:
			panic(fmt.Sprintf("demo: cell of type %T", v))
		}
	}
	b.WriteByte(')')
	return b.String()
}

// sqlEscape escapes s as mysqldump does inside a single-quoted string.
func sqlEscape(s string) string {
	var b strings.Builder
	for i := range len(s) {
		switch c := s[i]; c {
		case 0:
			b.WriteString(`\0`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case 0x1a:
			b.WriteString(`\Z`)
		case '\\', '\'', '"':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// phpString is s as PHP serialize writes a string: the length in bytes.
func phpString(s string) string {
	return "s:" + strconv.Itoa(len(s)) + `:"` + s + `";`
}

// phpArray serializes an array of string keys to already serialized
// values, in the order given.
func phpArray(kv ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "a:%d:{", len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteString(phpString(kv[i]))
		b.WriteString(kv[i+1])
	}
	b.WriteByte('}')
	return b.String()
}

// phpStrings serializes an array of string keys to string values.
func phpStrings(kv ...string) string {
	enc := make([]string, len(kv))
	for i, s := range kv {
		if i%2 == 1 {
			s = phpString(s)
		}
		enc[i] = s
	}
	return phpArray(enc...)
}
