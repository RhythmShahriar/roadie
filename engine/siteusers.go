// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package engine

import (
	"bytes"
	"strconv"
)

// siteUserRule is the change ID of the users and user meta rows copy
// --site leaves out.
const siteUserRule = "site-users"

// siteUsers limits copy --site to the members of the chosen blogs plus
// the network's super admins (decisions.md): pass 1 notes who belongs
// where, pass 2 drops every other user and their meta. The main site
// always stays, so its members do too. Pass 1 fills it in one goroutine;
// after seal it is only read.
type siteUsers struct {
	// capKeys are the usermeta keys that make a user a member of a
	// chosen blog: <prefix>capabilities and <prefix><N>_capabilities.
	capKeys map[string]bool
	members map[string]bool
	// logins maps every user's login to its ID, since site_admins names
	// super admins by login and a dump may hold it before or after the
	// users table.
	logins map[string]string
	admins []string
	// metaSeen is set once a usermeta row went by: without the table the
	// membership is unknown, so nobody is left out.
	metaSeen bool
	keep     map[string]bool // after seal: the user IDs that stay
}

// newSiteUsers returns the limiter for the chosen blog numbers, or nil
// when copy --site wasn't asked for.
func newSiteUsers(prefix string, blogs []int) *siteUsers {
	if len(blogs) == 0 || prefix == "" {
		return nil
	}
	s := &siteUsers{capKeys: map[string]bool{prefix + "capabilities": true}, members: map[string]bool{}, logins: map[string]string{}}
	for _, n := range blogs {
		if n != 1 {
			s.capKeys[prefix+strconv.Itoa(n)+"_capabilities"] = true
		}
	}
	return s
}

// observeMeta reads a usermeta row.
func (s *siteUsers) observeMeta(get func(string) ([]byte, bool)) {
	s.metaSeen = true
	key, _ := get("meta_key")
	if !s.capKeys[string(key)] {
		return
	}
	// WordPress deletes the key when a user leaves a blog; an empty role
	// list is no membership either.
	if v, _ := get("meta_value"); len(bytes.TrimSpace(v)) == 0 || bytes.Equal(bytes.TrimSpace(v), []byte("a:0:{}")) {
		return
	}
	if id, _ := get("user_id"); len(id) > 0 {
		s.members[string(id)] = true
	}
}

// observeUser reads a users row.
func (s *siteUsers) observeUser(get func(string) ([]byte, bool)) {
	id, _ := get("ID")
	login, _ := get("user_login")
	if len(id) > 0 && len(login) > 0 {
		s.logins[string(login)] = string(id)
	}
}

// observeSiteMeta reads a sitemeta row: site_admins is a serialized
// array of logins.
func (s *siteUsers) observeSiteMeta(get func(string) ([]byte, bool)) {
	if key, _ := get("meta_key"); string(key) != "site_admins" {
		return
	}
	v, _ := get("meta_value")
	s.admins = append(s.admins, serializedStrings(v)...)
}

// seal works out the IDs that stay: members, super admins and kept
// users. It drops the login map, which pass 2 doesn't need.
func (s *siteUsers) seal(kept *keptUsers) {
	if s == nil {
		return
	}
	s.keep = s.members
	for _, login := range s.admins {
		if id, ok := s.logins[login]; ok {
			s.keep[id] = true
		}
	}
	if kept != nil {
		for id := range kept.ids {
			s.keep[id] = true
		}
	}
	s.logins, s.members = nil, nil
}

// limits reports whether pass 2 leaves users out at all.
func (s *siteUsers) limits() bool { return s != nil && s.metaSeen }

// drops reports whether a row of table is a user or user meta row copy
// --site leaves out and the column that names the user.
func (s *siteUsers) drops(table, prefix string, get func(string) ([]byte, bool)) (string, bool) {
	if !s.limits() {
		return "", false
	}
	col := ""
	switch table {
	case prefix + "users":
		col = "ID"
	case prefix + "usermeta":
		col = "user_id"
	default:
		return "", false
	}
	id, _ := get(col)
	return col, len(id) > 0 && !s.keep[string(id)]
}

// serializedStrings returns the string elements of a PHP serialized
// value, as s:<len>:"<bytes>"; tokens. It reads by the length, so a
// quote inside a string doesn't end it and stops at the first token it
// can't read.
func serializedStrings(v []byte) []string {
	var out []string
	for {
		i := bytes.Index(v, []byte(`s:`))
		if i < 0 {
			return out
		}
		v = v[i+2:]
		j := bytes.IndexByte(v, ':')
		if j <= 0 {
			return out
		}
		n, err := strconv.Atoi(string(v[:j]))
		if err != nil || n < 0 || j+2+n+1 > len(v) || v[j+1] != '"' || v[j+2+n] != '"' {
			continue
		}
		out = append(out, string(v[j+2:j+2+n]))
		v = v[j+2+n+1:]
	}
}
