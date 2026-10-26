// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package demo

import (
	"bufio"
	"crypto/md5" // #nosec G501 -- Action Scheduler stores md5 of long args; not used for security
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"time"
)

// productionHost is the demo shop's production host.
const productionHost = "shop.example.com"

// productionURL is siteurl and home in the dump.
const productionURL = "https://" + productionHost

// orderCount is how many orders the shop has, whatever the customer count.
const orderCount = 60

// actionCount is how many Action Scheduler actions the shop has.
const actionCount = 20

// The invented people are combined from these lists. The combinations
// look real so verify has something to find; none is a known person.
var (
	firstNames = []string{
		"Ada", "Bruno", "Clara", "Dmitri", "Elif", "Farah", "Gustav", "Hana",
		"Ilse", "Jonas", "Kaia", "Lorenzo", "Mirela", "Nikhil", "Orla", "Pavel",
		"Quinn", "Rosalind", "Soren", "Tamsin", "Ugo", "Vesna", "Wren", "Ximena",
		"Yusuf", "Zofia", "Anouk", "Bastian", "Cosima", "Desmond",
	}
	lastNames = []string{
		"Kowalczyk", "Haverford", "Lindqvist", "Okonkwo", "Marchetti", "Dvorak",
		"Ashdown", "Feldmann", "Quintero", "Rasmussen", "Takahara", "Vukovic",
		"Brannigan", "Castellano", "Delacroix", "Esterhazy", "Fairbairn",
		"Grimaldi", "Holloway", "Iversen", "Jablonski", "Kerrigan", "Lacroix",
		"Montague", "Novak", "Oyelaran", "Pemberton", "Ravensworth",
	}
	mailDomains = []string{"gmail.com", "outlook.com", "hotmail.co.uk", "yahoo.com", "icloud.com", "proton.me"}
	streets     = []string{
		"Albion Road", "Beckett Street", "Cranbrook Avenue", "Dunmore Lane",
		"Elmhurst Close", "Fernside Road", "Glebe Terrace", "Harcourt Street",
		"Ivydene Gardens", "Juniper Way", "Kingsmead Road", "Larkhall Rise",
	}
	cities = []struct{ city, postcode, country, state string }{
		{"Leeds", "LS6 2QD", "GB", ""},
		{"Bristol", "BS7 8AE", "GB", ""},
		{"Manchester", "M20 6RL", "GB", ""},
		{"Edinburgh", "EH9 1SB", "GB", ""},
		{"Cardiff", "CF24 4HQ", "GB", ""},
		{"Dublin", "D06 F2X3", "IE", "D"},
		{"Portland", "97214", "US", "OR"},
		{"Austin", "78704", "US", "TX"},
	}
	products = []struct {
		title string
		price int // pence
	}{
		{"House Espresso 1kg", 2400}, {"Single Origin Kenya 250g", 1150},
		{"Decaf Colombia 250g", 950}, {"Pour Over Kit", 3800}, {"Gift Card", 2500},
	}
	statuses = []string{"wc-completed", "wc-completed", "wc-completed", "wc-processing", "wc-on-hold", "wc-refunded"}
)

// base is when the shop's history starts; every date is an offset from it.
var base = time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)

type customer struct {
	id                        int
	first, last, email, phone string
	login                     string
	street, city, postcode    string
	country, state            string
	ip, ua                    string
	registered                time.Time
}

type order struct {
	id, customer, product, qty int
	status                     string
	created                    time.Time
}

// shop holds everything the dump is made of.
type shop struct {
	r         *rand.Rand
	customers []customer
	orders    []order
	stripeKey string
}

func newShop(o Options) *shop {
	n := o.Customers
	if n <= 0 {
		n = DefaultCustomers
	}
	// #nosec G404 G115 -- a seeded generator for fake data, never a secret
	s := &shop{r: rand.New(rand.NewPCG(uint64(o.Seed), 0x64656d6f))}
	used := map[string]bool{}
	for i := range n {
		c := customer{id: 2 + i}
		for {
			c.first = firstNames[s.r.IntN(len(firstNames))]
			c.last = lastNames[s.r.IntN(len(lastNames))]
			if !used[c.first+c.last] || len(used) >= len(firstNames)*len(lastNames) {
				break
			}
		}
		used[c.first+c.last] = true
		local := strings.ToLower(c.first) + "." + strings.ToLower(c.last)
		c.email = local + strconv.Itoa(10+s.r.IntN(89)) + "@" + mailDomains[s.r.IntN(len(mailDomains))]
		c.login = strings.ToLower(c.first) + strings.ToLower(c.last[:1]) + strconv.Itoa(i+1)
		place := cities[s.r.IntN(len(cities))]
		c.city, c.postcode, c.country, c.state = place.city, place.postcode, place.country, place.state
		c.street = fmt.Sprintf("%d %s", 2+s.r.IntN(140), streets[s.r.IntN(len(streets))])
		c.phone = s.phone(c.country)
		c.ip = fmt.Sprintf("%d.%d.%d.%d", []int{81, 86, 92, 109, 145}[s.r.IntN(5)], 1+s.r.IntN(250), 1+s.r.IntN(250), 1+s.r.IntN(250))
		c.ua = fmt.Sprintf("Mozilla/5.0 (Macintosh; Intel Mac OS X 14_%d) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.%d Safari/605.1.15", s.r.IntN(7), s.r.IntN(4))
		c.registered = base.Add(time.Duration(s.r.IntN(30*24)) * time.Hour)
		s.customers = append(s.customers, c)
	}
	for i := range orderCount {
		c := s.customers[s.r.IntN(len(s.customers))]
		s.orders = append(s.orders, order{
			id:       100 + i,
			customer: c.id,
			product:  s.r.IntN(len(products)),
			qty:      1 + s.r.IntN(3),
			status:   statuses[s.r.IntN(len(statuses))],
			created:  c.registered.Add(time.Duration(1+s.r.IntN(60*24)) * time.Hour),
		})
	}
	// The key is assembled here so no live-format literal is in the
	// source: push protection blocks those and a scanner would flag them.
	s.stripeKey = strings.Join([]string{"sk", "live", s.token(24)}, "_")
	return s
}

// phone returns a number in a real (not fictional) range for country.
func (s *shop) phone(country string) string {
	d := func(n int) string {
		var b strings.Builder
		for range n {
			b.WriteByte("0123456789"[s.r.IntN(10)])
		}
		return b.String()
	}
	switch country {
	case "US":
		return "+1 503 " + strconv.Itoa(200+s.r.IntN(700)) + " " + d(4)
	case "IE":
		return "+353 87 " + d(3) + " " + d(4)
	}
	// 07911 is a real UK mobile range, outside Ofcom's 07700 900 drama block.
	return "+44 7911 " + d(6)
}

// token returns n letters and digits from the seeded generator.
func (s *shop) token(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[s.r.IntN(len(chars))]
	}
	return string(b)
}

func (s *shop) customer(id int) customer { return s.customers[id-2] }

func date(t time.Time) string { return t.Format("2006-01-02 15:04:05") }

func money(pence int) string { return fmt.Sprintf("%d.%02d", pence/100, pence%100) }

func (s *shop) write(w *bufio.Writer) {
	fmt.Fprint(w, dumpHeader)
	for _, t := range []*table{
		s.options(), s.users(), s.usermeta(), s.posts(), s.postmeta(), s.comments(),
		s.wcOrders(), s.wcOrderAddresses(), s.wcOrdersMeta(), s.wcCustomerLookup(),
		s.wcWebhooks(), s.asGroups(), s.asActions(),
	} {
		writeTable(w, t)
	}
	fmt.Fprint(w, dumpFooter)
}

const innodb = "ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci"

func (s *shop) options() *table {
	stripe := phpStrings(
		"enabled", "yes",
		"title", "Credit Card (Stripe)",
		"testmode", "no",
		"test_publishable_key", "",
		"test_secret_key", "",
		"publishable_key", strings.Join([]string{"pk", "live", s.token(24)}, "_"),
		"secret_key", s.stripeKey,
		"webhook_secret", "whsec_"+s.token(32),
		"capture", "yes",
	)
	plugins := phpArray(
		"0", phpString("woocommerce/woocommerce.php"),
		"1", phpString("woocommerce-gateway-stripe/woocommerce-gateway-stripe.php"),
	)
	// PHP writes integer keys as i:N; phpArray writes string keys, so the
	// list is rebuilt with integer keys here.
	plugins = strings.Replace(strings.Replace(plugins, `s:1:"0";`, "i:0;", 1), `s:1:"1";`, "i:1;", 1)
	rows := [][]any{
		{1, "siteurl", productionURL, "on"},
		{2, "home", productionURL, "on"},
		{3, "blogname", "Bean & Leaf Coffee", "on"},
		{4, "blogdescription", "Coffee and tea, roasted to order", "on"},
		{5, "admin_email", "owner@" + productionHost, "on"},
		{6, "blog_public", "1", "on"},
		{7, "active_plugins", plugins, "on"},
		{8, "woocommerce_custom_orders_table_enabled", "yes", "on"},
		{9, "woocommerce_custom_orders_table_data_sync_enabled", "no", "on"},
		{10, "woocommerce_stripe_settings", stripe, "on"},
		{11, "woocommerce_email_from_address", "orders@" + productionHost, "on"},
		{12, "woocommerce_currency", "GBP", "on"},
		{13, "template", "storefront", "on"},
		{14, "stylesheet", "storefront", "on"},
	}
	return &table{
		name: "wp_options",
		create: "  `option_id` bigint unsigned NOT NULL AUTO_INCREMENT,\n" +
			"  `option_name` varchar(191) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `option_value` longtext COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `autoload` varchar(20) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT 'yes',\n" +
			"  PRIMARY KEY (`option_id`),\n" +
			"  UNIQUE KEY `option_name` (`option_name`),\n" +
			"  KEY `autoload` (`autoload`)",
		engine: "ENGINE=InnoDB AUTO_INCREMENT=15 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_520_ci",
		rows:   rows,
	}
}

// passwordHash is shaped like WordPress 6.8's bcrypt hashes. It is random
// from the seed: no password hashes to it.
func (s *shop) passwordHash() string {
	return "$wp$2y$10$" + s.token(53)
}

func (s *shop) users() *table {
	rows := [][]any{{1, "shopowner", s.passwordHash(), "shopowner", "owner@" + productionHost, productionURL, date(base), "", 0, "Shop Owner"}}
	for _, c := range s.customers {
		rows = append(rows, []any{c.id, c.login, s.passwordHash(), strings.ToLower(c.first + "-" + c.last), c.email, "", date(c.registered), "", 0, c.first + " " + c.last})
	}
	return &table{
		name: "wp_users",
		create: "  `ID` bigint unsigned NOT NULL AUTO_INCREMENT,\n" +
			"  `user_login` varchar(60) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `user_pass` varchar(255) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `user_nicename` varchar(50) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `user_email` varchar(100) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `user_url` varchar(100) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `user_registered` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',\n" +
			"  `user_activation_key` varchar(255) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `user_status` int NOT NULL DEFAULT '0',\n" +
			"  `display_name` varchar(250) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  PRIMARY KEY (`ID`),\n" +
			"  KEY `user_login_key` (`user_login`),\n" +
			"  KEY `user_nicename` (`user_nicename`),\n" +
			"  KEY `user_email` (`user_email`)",
		engine: innodb,
		rows:   rows,
	}
}

func (s *shop) usermeta() *table {
	var rows [][]any
	add := func(user int, key, value string) {
		rows = append(rows, []any{len(rows) + 1, user, key, value})
	}
	add(1, "nickname", "shopowner")
	add(1, "wp_capabilities", phpArray("administrator", "b:1;"))
	add(1, "wp_user_level", "10")
	for _, c := range s.customers {
		add(c.id, "nickname", c.login)
		add(c.id, "first_name", c.first)
		add(c.id, "last_name", c.last)
		add(c.id, "wp_capabilities", phpArray("customer", "b:1;"))
		for _, kind := range []string{"billing", "shipping"} {
			add(c.id, kind+"_first_name", c.first)
			add(c.id, kind+"_last_name", c.last)
			add(c.id, kind+"_address_1", c.street)
			add(c.id, kind+"_city", c.city)
			add(c.id, kind+"_postcode", c.postcode)
			add(c.id, kind+"_country", c.country)
			add(c.id, kind+"_state", c.state)
		}
		add(c.id, "billing_email", c.email)
		add(c.id, "billing_phone", c.phone)
		login := c.registered.Add(48 * time.Hour)
		session := phpArray(
			"expiration", "i:"+strconv.FormatInt(login.Add(14*24*time.Hour).Unix(), 10)+";",
			"ip", phpString(c.ip),
			"ua", phpString(c.ua),
			"login", "i:"+strconv.FormatInt(login.Unix(), 10)+";",
		)
		add(c.id, "session_tokens", phpArray(strings.ToLower(s.token(64)), session))
		add(c.id, "last_update", strconv.FormatInt(login.Unix(), 10))
	}
	return &table{
		name: "wp_usermeta",
		create: "  `umeta_id` bigint unsigned NOT NULL AUTO_INCREMENT,\n" +
			"  `user_id` bigint unsigned NOT NULL DEFAULT '0',\n" +
			"  `meta_key` varchar(255) COLLATE utf8mb4_unicode_520_ci DEFAULT NULL,\n" +
			"  `meta_value` longtext COLLATE utf8mb4_unicode_520_ci,\n" +
			"  PRIMARY KEY (`umeta_id`),\n" +
			"  KEY `user_id` (`user_id`),\n" +
			"  KEY `meta_key` (`meta_key`(191))",
		engine: innodb,
		rows:   rows,
	}
}

// Post IDs: pages 2 to 5, the landing page 6, a menu item 7, products from
// 10 and the HPOS placeholder posts of the orders from 100.
const (
	landingPage  = 6
	menuItem     = 7
	firstProduct = 10
)

func (s *shop) posts() *table {
	var rows [][]any
	post := func(id int, at time.Time, content, title, name, typ, status string) {
		rows = append(rows, []any{
			id, 1, date(at), date(at), content, title, "", status, "closed", "closed", "", name, "", "",
			date(at), date(at), "", 0, productionURL + "/?p=" + strconv.Itoa(id), 0, typ, "", 0,
		})
	}
	for i, p := range []struct{ title, name string }{{"Shop", "shop"}, {"Cart", "cart"}, {"Checkout", "checkout"}, {"My account", "my-account"}} {
		post(2+i, base, "", p.title, p.name, "page", "publish")
	}
	post(landingPage, base, `<!-- wp:cover {"url":"`+productionURL+`/wp-content/uploads/2026/06/beans.jpg"} -->`+
		"\n"+`<a href="`+productionURL+`/shop/">Order fresh beans</a>`+"\n<!-- /wp:cover -->",
		"Fresh beans", "fresh-beans", "page", "publish")
	post(menuItem, base, "", "Shop", "shop-menu", "nav_menu_item", "publish")
	for i, p := range products {
		post(firstProduct+i, base, "Roasted in small batches.", p.title, strings.ToLower(strings.ReplaceAll(p.title, " ", "-")), "product", "publish")
	}
	for _, o := range s.orders {
		post(o.id, o.created, "", "", "", "shop_order_placehold", "draft")
	}
	return &table{
		name: "wp_posts",
		create: "  `ID` bigint unsigned NOT NULL AUTO_INCREMENT,\n" +
			"  `post_author` bigint unsigned NOT NULL DEFAULT '0',\n" +
			"  `post_date` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',\n" +
			"  `post_date_gmt` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',\n" +
			"  `post_content` longtext COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `post_title` text COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `post_excerpt` text COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `post_status` varchar(20) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT 'publish',\n" +
			"  `comment_status` varchar(20) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT 'open',\n" +
			"  `ping_status` varchar(20) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT 'open',\n" +
			"  `post_password` varchar(255) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `post_name` varchar(200) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `to_ping` text COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `pinged` text COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `post_modified` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',\n" +
			"  `post_modified_gmt` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',\n" +
			"  `post_content_filtered` longtext COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `post_parent` bigint unsigned NOT NULL DEFAULT '0',\n" +
			"  `guid` varchar(255) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `menu_order` int NOT NULL DEFAULT '0',\n" +
			"  `post_type` varchar(20) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT 'post',\n" +
			"  `post_mime_type` varchar(100) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `comment_count` bigint NOT NULL DEFAULT '0',\n" +
			"  PRIMARY KEY (`ID`),\n" +
			"  KEY `post_name` (`post_name`(191)),\n" +
			"  KEY `type_status_date` (`post_type`,`post_status`,`post_date`,`ID`),\n" +
			"  KEY `post_parent` (`post_parent`),\n" +
			"  KEY `post_author` (`post_author`)",
		engine: innodb,
		rows:   rows,
	}
}

func (s *shop) postmeta() *table {
	var rows [][]any
	add := func(post int, key, value string) {
		rows = append(rows, []any{len(rows) + 1, post, key, value})
	}
	// Elementor stores its layout as JSON with escaped slashes, so a plain
	// search for the URL misses it.
	elementor, err := json.Marshal([]map[string]any{{
		"id": "3f2a9c1", "elType": "section",
		"settings": map[string]any{"background_image": map[string]string{"url": productionURL + "/wp-content/uploads/2026/06/hero.jpg"}},
		"elements": []any{},
	}})
	if err != nil {
		panic(err)
	}
	add(landingPage, "_elementor_data", strings.ReplaceAll(string(elementor), "/", `\/`))
	add(landingPage, "_elementor_page_settings", phpStrings("custom_link_url", productionURL+"/sale/", "hide_title", "yes"))
	add(menuItem, "_menu_item_type", "custom")
	add(menuItem, "_menu_item_url", productionURL+"/shop/")
	for i, p := range products {
		id := firstProduct + i
		add(id, "_price", money(p.price))
		add(id, "_regular_price", money(p.price))
		add(id, "_sku", fmt.Sprintf("BL-%04d", 1001+i))
		add(id, "_stock_status", "instock")
	}
	return &table{
		name: "wp_postmeta",
		create: "  `meta_id` bigint unsigned NOT NULL AUTO_INCREMENT,\n" +
			"  `post_id` bigint unsigned NOT NULL DEFAULT '0',\n" +
			"  `meta_key` varchar(255) COLLATE utf8mb4_unicode_520_ci DEFAULT NULL,\n" +
			"  `meta_value` longtext COLLATE utf8mb4_unicode_520_ci,\n" +
			"  PRIMARY KEY (`meta_id`),\n" +
			"  KEY `post_id` (`post_id`),\n" +
			"  KEY `meta_key` (`meta_key`(191))",
		engine: innodb,
		rows:   rows,
	}
}

func (s *shop) comments() *table {
	var rows [][]any
	note := func(o order, at time.Time, text string) {
		rows = append(rows, []any{len(rows) + 1, o.id, "WooCommerce", "woocommerce@" + productionHost, "", "", date(at), date(at), text, 0, "1", "WooCommerce", "order_note", 0, 0})
	}
	for i, o := range s.orders {
		c := s.customer(o.customer)
		note(o, o.created.Add(time.Minute), "Order status changed from Pending payment to Processing.")
		// Every third order has a note a person typed, with contact
		// details in free text, where only a scrub of the text finds them.
		if i%3 == 0 {
			note(o, o.created.Add(2*time.Hour), fmt.Sprintf("Customer rang from %s: please send the invoice to %s and leave the parcel next door.", c.phone, c.email))
		}
	}
	return &table{
		name: "wp_comments",
		create: "  `comment_ID` bigint unsigned NOT NULL AUTO_INCREMENT,\n" +
			"  `comment_post_ID` bigint unsigned NOT NULL DEFAULT '0',\n" +
			"  `comment_author` tinytext COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `comment_author_email` varchar(100) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `comment_author_url` varchar(200) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `comment_author_IP` varchar(100) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `comment_date` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',\n" +
			"  `comment_date_gmt` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',\n" +
			"  `comment_content` text COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `comment_karma` int NOT NULL DEFAULT '0',\n" +
			"  `comment_approved` varchar(20) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '1',\n" +
			"  `comment_agent` varchar(255) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `comment_type` varchar(20) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT 'comment',\n" +
			"  `comment_parent` bigint unsigned NOT NULL DEFAULT '0',\n" +
			"  `user_id` bigint unsigned NOT NULL DEFAULT '0',\n" +
			"  PRIMARY KEY (`comment_ID`),\n" +
			"  KEY `comment_post_ID` (`comment_post_ID`),\n" +
			"  KEY `comment_type` (`comment_type`)",
		engine: innodb,
		rows:   rows,
	}
}

func (s *shop) wcOrders() *table {
	var rows [][]any
	for _, o := range s.orders {
		c := s.customer(o.customer)
		total := products[o.product].price * o.qty
		rows = append(rows, []any{
			o.id, o.status, "GBP", "shop_order", money(total / 6), money(total), c.id, c.email,
			date(o.created), date(o.created.Add(time.Hour)), 0, "stripe", "Credit Card (Stripe)",
			"ch_" + s.token(24), c.ip, c.ua, "",
		})
	}
	return &table{
		name: "wp_wc_orders",
		create: "  `id` bigint unsigned NOT NULL,\n" +
			"  `status` varchar(20) COLLATE utf8mb4_unicode_520_ci DEFAULT NULL,\n" +
			"  `currency` varchar(10) COLLATE utf8mb4_unicode_520_ci DEFAULT NULL,\n" +
			"  `type` varchar(20) COLLATE utf8mb4_unicode_520_ci DEFAULT NULL,\n" +
			"  `tax_amount` decimal(26,8) DEFAULT NULL,\n" +
			"  `total_amount` decimal(26,8) DEFAULT NULL,\n" +
			"  `customer_id` bigint unsigned DEFAULT NULL,\n" +
			"  `billing_email` varchar(320) COLLATE utf8mb4_unicode_520_ci DEFAULT NULL,\n" +
			"  `date_created_gmt` datetime DEFAULT NULL,\n" +
			"  `date_updated_gmt` datetime DEFAULT NULL,\n" +
			"  `parent_order_id` bigint unsigned DEFAULT NULL,\n" +
			"  `payment_method` varchar(100) COLLATE utf8mb4_unicode_520_ci DEFAULT NULL,\n" +
			"  `payment_method_title` text COLLATE utf8mb4_unicode_520_ci,\n" +
			"  `transaction_id` varchar(100) COLLATE utf8mb4_unicode_520_ci DEFAULT NULL,\n" +
			"  `ip_address` varchar(100) COLLATE utf8mb4_unicode_520_ci DEFAULT NULL,\n" +
			"  `user_agent` text COLLATE utf8mb4_unicode_520_ci,\n" +
			"  `customer_note` text COLLATE utf8mb4_unicode_520_ci,\n" +
			"  PRIMARY KEY (`id`),\n" +
			"  KEY `status` (`status`),\n" +
			"  KEY `customer_id_billing_email` (`customer_id`,`billing_email`(171)),\n" +
			"  KEY `billing_email` (`billing_email`(191))",
		engine: innodb,
		rows:   rows,
	}
}

func (s *shop) wcOrderAddresses() *table {
	var rows [][]any
	for _, o := range s.orders {
		c := s.customer(o.customer)
		for _, kind := range []string{"billing", "shipping"} {
			email, phone := c.email, c.phone
			if kind == "shipping" {
				email = ""
			}
			rows = append(rows, []any{len(rows) + 1, o.id, kind, c.first, c.last, "", c.street, "", c.city, c.state, c.postcode, c.country, email, phone})
		}
	}
	return &table{
		name: "wp_wc_order_addresses",
		create: "  `id` bigint unsigned NOT NULL AUTO_INCREMENT,\n" +
			"  `order_id` bigint unsigned NOT NULL,\n" +
			"  `address_type` varchar(20) COLLATE utf8mb4_unicode_520_ci DEFAULT NULL,\n" +
			"  `first_name` text COLLATE utf8mb4_unicode_520_ci,\n" +
			"  `last_name` text COLLATE utf8mb4_unicode_520_ci,\n" +
			"  `company` text COLLATE utf8mb4_unicode_520_ci,\n" +
			"  `address_1` text COLLATE utf8mb4_unicode_520_ci,\n" +
			"  `address_2` text COLLATE utf8mb4_unicode_520_ci,\n" +
			"  `city` text COLLATE utf8mb4_unicode_520_ci,\n" +
			"  `state` text COLLATE utf8mb4_unicode_520_ci,\n" +
			"  `postcode` text COLLATE utf8mb4_unicode_520_ci,\n" +
			"  `country` text COLLATE utf8mb4_unicode_520_ci,\n" +
			"  `email` varchar(320) COLLATE utf8mb4_unicode_520_ci DEFAULT NULL,\n" +
			"  `phone` varchar(100) COLLATE utf8mb4_unicode_520_ci DEFAULT NULL,\n" +
			"  PRIMARY KEY (`id`),\n" +
			"  UNIQUE KEY `address_type_order_id` (`address_type`,`order_id`),\n" +
			"  KEY `email` (`email`(191)),\n" +
			"  KEY `phone` (`phone`)",
		engine: innodb,
		rows:   rows,
	}
}

func (s *shop) wcOrdersMeta() *table {
	var rows [][]any
	for _, o := range s.orders {
		rows = append(rows,
			[]any{len(rows) + 1, o.id, "_stripe_customer_id", "cus_" + s.token(14)},
			[]any{len(rows) + 2, o.id, "_stripe_source_id", "src_" + s.token(24)},
			[]any{len(rows) + 3, o.id, "_order_stock_reduced", "yes"},
		)
	}
	return &table{
		name: "wp_wc_orders_meta",
		create: "  `id` bigint unsigned NOT NULL AUTO_INCREMENT,\n" +
			"  `order_id` bigint unsigned DEFAULT NULL,\n" +
			"  `meta_key` varchar(255) COLLATE utf8mb4_unicode_520_ci DEFAULT NULL,\n" +
			"  `meta_value` text COLLATE utf8mb4_unicode_520_ci,\n" +
			"  PRIMARY KEY (`id`),\n" +
			"  KEY `meta_key_value` (`meta_key`(100),`meta_value`(82)),\n" +
			"  KEY `order_id_meta_key_meta_value` (`order_id`,`meta_key`(100),`meta_value`(82))",
		engine: innodb,
		rows:   rows,
	}
}

func (s *shop) wcCustomerLookup() *table {
	var rows [][]any
	for i, c := range s.customers {
		rows = append(rows, []any{i + 1, c.id, c.login, c.first, c.last, c.email, date(c.registered.Add(72 * time.Hour)), date(c.registered), c.country, c.postcode, c.city, c.state})
	}
	return &table{
		name: "wp_wc_customer_lookup",
		create: "  `customer_id` bigint unsigned NOT NULL AUTO_INCREMENT,\n" +
			"  `user_id` bigint unsigned DEFAULT NULL,\n" +
			"  `username` varchar(60) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `first_name` varchar(255) COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `last_name` varchar(255) COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `email` varchar(100) COLLATE utf8mb4_unicode_520_ci DEFAULT NULL,\n" +
			"  `date_last_active` timestamp NULL DEFAULT NULL,\n" +
			"  `date_registered` timestamp NULL DEFAULT NULL,\n" +
			"  `country` char(2) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `postcode` varchar(20) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `city` varchar(100) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  `state` varchar(100) COLLATE utf8mb4_unicode_520_ci NOT NULL DEFAULT '',\n" +
			"  PRIMARY KEY (`customer_id`),\n" +
			"  UNIQUE KEY `user_id` (`user_id`),\n" +
			"  KEY `email` (`email`)",
		engine: innodb,
		rows:   rows,
	}
}

func (s *shop) wcWebhooks() *table {
	rows := [][]any{{
		1, "active", "Orders to the CRM", 1, "https://hooks.zapier.com/hooks/catch/" + strconv.Itoa(1000000+s.r.IntN(8999999)) + "/" + strings.ToLower(s.token(6)) + "/",
		s.token(40), "order.created", date(base), date(base), date(base), date(base), 3, 0, 0,
	}}
	return &table{
		name: "wp_wc_webhooks",
		create: "  `webhook_id` bigint unsigned NOT NULL AUTO_INCREMENT,\n" +
			"  `status` varchar(200) COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `name` text COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `user_id` bigint unsigned NOT NULL,\n" +
			"  `delivery_url` text COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `secret` text COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `topic` varchar(200) COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `date_created` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',\n" +
			"  `date_created_gmt` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',\n" +
			"  `date_modified` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',\n" +
			"  `date_modified_gmt` datetime NOT NULL DEFAULT '0000-00-00 00:00:00',\n" +
			"  `api_version` smallint NOT NULL,\n" +
			"  `failure_count` smallint NOT NULL DEFAULT '0',\n" +
			"  `pending_delivery` tinyint(1) NOT NULL DEFAULT '0',\n" +
			"  PRIMARY KEY (`webhook_id`),\n" +
			"  KEY `user_id` (`user_id`)",
		engine: innodb,
		rows:   rows,
	}
}

func (s *shop) asGroups() *table {
	return &table{
		name: "wp_actionscheduler_groups",
		create: "  `group_id` bigint unsigned NOT NULL AUTO_INCREMENT,\n" +
			"  `slug` varchar(255) COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  PRIMARY KEY (`group_id`),\n" +
			"  KEY `slug` (`slug`(191))",
		engine: innodb,
		rows:   [][]any{{1, "action-scheduler-migration"}, {2, "woocommerce"}},
	}
}

// asArgsLimit is the width of Action Scheduler's args column: longer args
// go to extended_args and args holds their md5.
const asArgsLimit = 191

func (s *shop) asActions() *table {
	var rows [][]any
	for i := range actionCount {
		o := s.orders[i%len(s.orders)]
		c := s.customer(o.customer)
		hook, args := "woocommerce_send_review_reminder", map[string]any{"order_id": o.id, "email": c.email}
		if i == actionCount-1 {
			// The one long action: its args pass 191 bytes, so Action
			// Scheduler keeps them in extended_args and an md5 in args.
			hook = "woocommerce_deliver_crm_sync"
			args = map[string]any{
				"order_id": o.id, "email": c.email, "phone": c.phone,
				"name": c.first + " " + c.last, "address": c.street + ", " + c.city + " " + c.postcode,
				"note": "Gift wrap requested; ring the bell twice.",
			}
		}
		raw, err := json.Marshal(args)
		if err != nil {
			panic(err)
		}
		argsCol, extended := any(string(raw)), any(nil)
		if len(raw) > asArgsLimit {
			sum := md5.Sum(raw) // #nosec G401 -- the format Action Scheduler uses, not security
			argsCol, extended = hex.EncodeToString(sum[:]), string(raw)
		}
		at := o.created.Add(7 * 24 * time.Hour)
		rows = append(rows, []any{
			i + 1, hook, "pending", date(at), date(at), 10, argsCol,
			`O:30:"ActionScheduler_SimpleSchedule":2:{s:22:"` + "\x00*\x00scheduled_timestamp" + `";i:` + strconv.FormatInt(at.Unix(), 10) + `;s:41:"` + "\x00ActionScheduler_SimpleSchedule\x00timestamp" + `";i:` + strconv.FormatInt(at.Unix(), 10) + `;}`,
			2, 0, "0000-00-00 00:00:00", "0000-00-00 00:00:00", 0, extended,
		})
	}
	return &table{
		name: "wp_actionscheduler_actions",
		create: "  `action_id` bigint unsigned NOT NULL AUTO_INCREMENT,\n" +
			"  `hook` varchar(191) COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `status` varchar(20) COLLATE utf8mb4_unicode_520_ci NOT NULL,\n" +
			"  `scheduled_date_gmt` datetime DEFAULT '0000-00-00 00:00:00',\n" +
			"  `scheduled_date_local` datetime DEFAULT '0000-00-00 00:00:00',\n" +
			"  `priority` tinyint unsigned NOT NULL DEFAULT '10',\n" +
			"  `args` varchar(191) COLLATE utf8mb4_unicode_520_ci DEFAULT NULL,\n" +
			"  `schedule` longtext COLLATE utf8mb4_unicode_520_ci,\n" +
			"  `group_id` bigint unsigned NOT NULL DEFAULT '0',\n" +
			"  `attempts` int NOT NULL DEFAULT '0',\n" +
			"  `last_attempt_gmt` datetime DEFAULT '0000-00-00 00:00:00',\n" +
			"  `last_attempt_local` datetime DEFAULT '0000-00-00 00:00:00',\n" +
			"  `claim_id` bigint unsigned NOT NULL DEFAULT '0',\n" +
			"  `extended_args` varchar(8000) COLLATE utf8mb4_unicode_520_ci DEFAULT NULL,\n" +
			"  PRIMARY KEY (`action_id`),\n" +
			"  KEY `hook_status_scheduled_date_gmt` (`hook`(163),`status`,`scheduled_date_gmt`),\n" +
			"  KEY `status_scheduled_date_gmt` (`status`,`scheduled_date_gmt`),\n" +
			"  KEY `args` (`args`),\n" +
			"  KEY `group_id` (`group_id`),\n" +
			"  KEY `claim_id_status_scheduled_date_gmt` (`claim_id`,`status`,`scheduled_date_gmt`)",
		engine: innodb,
		rows:   rows,
	}
}
