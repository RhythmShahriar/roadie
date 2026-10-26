# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Roadie's sealed testbed (see docker-compose.yml). Needs Docker with
# Compose v2 and Go. Ports: the gate publishes 127.0.0.1:18920-18939 only
# (nginx.conf lists them); Go test listeners use 127.0.0.1:23240-23259.
#
#   make up                build, start, install the sites, seed the canaries and keys
#   make down              stop and delete everything, volumes included
#   make reset ID=r01      reseed one scenario and load its input, clear the logs
#   make gen [GEN_SIZE=20GB]  r17's large dumps (make up writes 2GB)
#   make formats           r18's shop dumped by the real mysqldump, mariadb-dump
#                          and wp db export (make up runs it)
#   make seal              TestNetworkIsSealed against the running testbed
#   make oracle ID=r01 DUMP=out.sql [TREE=dir]   judge an output
#   make bench [ONLY=r01]  the benchmark into bench/work/results.json
#   make reproduce         the benchmark twice; fails if the score differs
#   make drill             the side-effect drill alone on the running staging
#
# Nothing here prints a secret: the passwords, Echo's token, the SSH keys
# and the fake API keys are written to runtime/ (0600, gitignored) and
# read from there.

COMPOSE := docker compose -f docker-compose.yml
GO      ?= $(shell command -v go 2>/dev/null || echo /usr/local/go/bin/go)
RUNTIME := runtime
ROOT    := $(abspath ..)
export GOWORK := off
# The sinkhole and the stubs run as root; their logs are handed to you.
export TESTBED_UID := $(shell id -u)
export TESTBED_GID := $(shell id -g)

# The other tools and plugins, pinned: "<url> <sha256> <folder>" per line.
# A new version means a new line here and a new sha256, checked at build.
define PLUGINS
https://downloads.wordpress.org/plugin/woocommerce.9.8.5.zip 9008e72eacda8f3bda01d0b704502eaddd8bc147651d61bd9aa7a015bf583964 woocommerce
https://downloads.wordpress.org/plugin/wp-mail-smtp.4.4.0.zip f4b752fe3842df1b47f86080fcf616afe93977db0eb35c8e45a84fba56f93922 wp-mail-smtp
https://downloads.wordpress.org/plugin/wp-mail-logging.1.14.0.zip 57954bf403fe91af69ba07707846aca61c3c5dc9c4207f2db3c5883d5937925b wp-mail-logging
https://downloads.wordpress.org/plugin/fluent-smtp.2.2.82.zip 2334dc885f0c0a6fc17ab979d7f3b62c832ac4f9510b64887100d85e3451a29e fluent-smtp
https://github.com/Automattic/woocommerce-subscriptions-core/archive/refs/tags/8.2.0.zip 1d2a898b65eb76473558d797407b3e41a0a3cbf898c4a2e5ae24e1df4ccf893e woocommerce-subscriptions-core
endef
export TESTBED_PLUGINS := $(PLUGINS)

# PHP for the seed-time serialized list and the oracle's integrity check,
# in a container so the host needs no PHP.
PHP_IMAGE := php:8.3-cli@sha256:98d555ff97b3865140ad31032508c3ca089fa97a8944b14ca9dc03ea05d788cd
PHP       := docker run --rm -i --network none $(PHP_IMAGE) php
SEED      ?= 1

.PHONY: up down reset seal oracle bench reproduce drill roadie-bin binaries keys seed gen install load formats status logs

up: binaries keys seed gen ## build, start and seed everything
	$(COMPOSE) build -q sinkhole prod-wp laravel sshd
	$(COMPOSE) up -d --wait
	$(MAKE) --no-print-directory install formats load ID=r01
	@echo "prod-wp:    http://127.0.0.1:18920/"
	@echo "staging-wp: http://127.0.0.1:18921/"
	@echo "Laravel:    http://127.0.0.1:18922/"
	@echo "Mailpit:    http://127.0.0.1:18923/"
	@echo "ssh:        roadie@127.0.0.1 -p 18925 (key runtime/ssh/id_ed25519, known_hosts runtime/known_hosts)"

down: ## stop and delete everything; fails if a volume is left
	$(COMPOSE) down -v --remove-orphans
	@left=$$(docker volume ls -q --filter label=com.docker.compose.project=roadie-testbed); \
	if [ -n "$$left" ]; then echo "volumes left after down: $$left" >&2; exit 1; fi

# The testbed's own programs, static for the runner image, from this
# checkout; Echo at the version go.mod pins.
binaries:
	@mkdir -p $(RUNTIME)/bin $(RUNTIME)/log
	cd $(ROOT) && CGO_ENABLED=0 GOOS=linux $(GO) build -o testbed/$(RUNTIME)/bin/sinkhole ./testbed/sinkhole
	cd $(ROOT) && CGO_ENABLED=0 GOOS=linux $(GO) build -o testbed/$(RUNTIME)/bin/stubs ./testbed/stubs
	cd $(ROOT) && CGO_ENABLED=0 GOOS=linux $(GO) build -o testbed/$(RUNTIME)/bin/rhythm-echo github.com/RhythmShahriar/echo/cmd/rhythm-echo

# Fresh secrets every make up, written 0600 and never echoed: the
# database passwords, Echo's token, the SSH host and client keys and the
# stubs' CA and host certificate.
keys:
	@mkdir -p $(RUNTIME)/ssh && chmod 700 $(RUNTIME) $(RUNTIME)/ssh
	@umask 077; pw=$$(od -An -N18 -tx1 /dev/urandom | tr -d ' \n'); \
	for k in MYSQL_ROOT_PASSWORD MYSQL_PASSWORD MARIADB_ROOT_PASSWORD MARIADB_PASSWORD WORDPRESS_DB_PASSWORD POSTGRES_PASSWORD DB_PASSWORD; do \
		echo "$$k=$$pw"; done > $(RUNTIME)/db.env
	@umask 077; echo "RHYTHM_ECHO_TOKEN=$$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')" > $(RUNTIME)/echo.env
	@rm -f $(RUNTIME)/ssh/ssh_host_ed25519_key* $(RUNTIME)/ssh/id_ed25519*
	@$(COMPOSE) build -q sshd
	@docker run --rm --network none --user $$(id -u):$$(id -g) --entrypoint ssh-keygen -v $(CURDIR)/$(RUNTIME)/ssh:/k roadie-testbed-sshd:1 -q -t ed25519 -N '' -C roadie-testbed-host -f /k/ssh_host_ed25519_key
	@docker run --rm --network none --user $$(id -u):$$(id -g) --entrypoint ssh-keygen -v $(CURDIR)/$(RUNTIME)/ssh:/k roadie-testbed-sshd:1 -q -t ed25519 -N '' -C roadie-testbed-client -f /k/id_ed25519
	@umask 077; echo "[127.0.0.1]:18925 $$(cut -d' ' -f1,2 $(RUNTIME)/ssh/ssh_host_ed25519_key.pub)" > $(RUNTIME)/known_hosts
	@rm -rf $(RUNTIME)/stubs && cd $(ROOT) && $(GO) run ./testbed/stubs -init -dir testbed/$(RUNTIME)/stubs
	@echo "keys: new database passwords, Echo token, SSH keys and stubs CA in $(RUNTIME)/"

seed: ## fill every scenario template: runtime/fixtures/, runtime/canaries.json
	cd $(ROOT) && $(GO) run ./testbed/seed/cmd/seed -fixtures testbed/fixtures -out testbed/$(RUNTIME) -seed $(SEED) -php "$(PHP)"

# r17's large dumps (testbed/gen): GEN_SIZE of SQL in db.sql.gz (2GB here,
# 20GB in the nightly) and 200MB in db-small.sql.gz for the memory
# comparison. A stamp keeps make up from writing them again at one size.
GEN_SIZE ?= 2GB
GEN_DIR  := $(RUNTIME)/fixtures/r17
gen: ## r17: the large generated dumps (GEN_SIZE=2GB)
	@if [ -f $(GEN_DIR)/.size-$(GEN_SIZE)-$(SEED) ] && grep -q '"r17' $(RUNTIME)/canaries.json; then \
		echo "gen: $(GEN_DIR) already holds $(GEN_SIZE)"; \
	else \
		rm -rf $(GEN_DIR) && cd $(ROOT) && $(GO) run ./testbed/gen/cmd/gen -size $(GEN_SIZE) -small 200MB -out testbed/$(GEN_DIR) -runtime testbed/$(RUNTIME) -seed $(SEED) \
		&& touch testbed/$(GEN_DIR)/.size-$(GEN_SIZE)-$(SEED); \
	fi

install: ## install WordPress and the plugins on both sites
	docker exec -u www-data roadie-testbed-prod-wp-1 testbed-seed
	docker exec -u www-data roadie-testbed-staging-wp-1 testbed-seed

# load ID=<fixture>: the fixture's db.sql into prod-db (MySQL) or pg.
load:
	@test -n "$(ID)" || { echo "usage: make load ID=r01" >&2; exit 2; }
	@test -f $(RUNTIME)/fixtures/$(ID)/db.sql || { echo "no $(RUNTIME)/fixtures/$(ID)/db.sql: run make seed" >&2; exit 1; }
	@if grep -q "PostgreSQL database dump" $(RUNTIME)/fixtures/$(ID)/db.sql; then \
		docker exec -i roadie-testbed-pg-1 psql -q -U app -d app -v ON_ERROR_STOP=1 < $(RUNTIME)/fixtures/$(ID)/db.sql; \
	else \
		docker exec -i roadie-testbed-prod-db-1 sh -c 'MYSQL_PWD="$$MYSQL_PASSWORD" exec mysql -ushop shop' < $(RUNTIME)/fixtures/$(ID)/db.sql; \
	fi
	@echo "loaded $(ID)"

# r18's real-tool cases: the shop loaded into a scratch database "formats"
# on prod-db (MySQL 8.4) and staging-db (MariaDB 11.4), then dumped by
# each tool as a user would run it. Root's password comes from the
# container's own environment, never a command line.
FORMATS := $(RUNTIME)/fixtures/r18
formats: ## r18: the same shop dumped by mysqldump, mariadb-dump and wp db export
	@test -f $(FORMATS)/db.sql || { echo "no $(FORMATS)/db.sql: run make seed" >&2; exit 1; }
	@docker exec roadie-testbed-prod-db-1 sh -c 'MYSQL_PWD="$$MYSQL_ROOT_PASSWORD" exec mysql -uroot -e "DROP DATABASE IF EXISTS formats; CREATE DATABASE formats; GRANT ALL ON formats.* TO shop"'
	@docker exec -i roadie-testbed-prod-db-1 sh -c 'MYSQL_PWD="$$MYSQL_PASSWORD" exec mysql -ushop formats' < $(FORMATS)/db.sql
	@umask 077; docker exec roadie-testbed-prod-db-1 sh -c 'MYSQL_PWD="$$MYSQL_PASSWORD" exec mysqldump -ushop --single-transaction --no-tablespaces --hex-blob formats' > $(FORMATS)/tool-mysqldump.sql
	@umask 077; docker exec -u roadie -e WORDPRESS_DB_NAME=formats roadie-testbed-sshd-1 wp --path=/var/www/html db export - --skip-plugins --skip-themes > $(FORMATS)/tool-wp-db-export.sql
	@docker exec roadie-testbed-staging-db-1 sh -c 'MYSQL_PWD="$$MARIADB_ROOT_PASSWORD" exec mariadb -uroot -e "DROP DATABASE IF EXISTS formats; CREATE DATABASE formats; GRANT ALL ON formats.* TO shop"'
	@docker exec -i roadie-testbed-staging-db-1 sh -c 'MYSQL_PWD="$$MARIADB_PASSWORD" exec mariadb -ushop formats' < $(FORMATS)/db.sql
	@umask 077; docker exec roadie-testbed-staging-db-1 sh -c 'MYSQL_PWD="$$MARIADB_PASSWORD" exec mariadb-dump -ushop --single-transaction formats' > $(FORMATS)/tool-mariadb-dump.sql
	@echo "formats: tool-mysqldump.sql, tool-mariadb-dump.sql and tool-wp-db-export.sql in $(FORMATS)/"

reset: ## reseed one scenario (ID=r01), load its input and clear the sinkhole and stub logs
	@test -n "$(ID)" || { echo "usage: make reset ID=r01" >&2; exit 2; }
	cd $(ROOT) && $(GO) run ./testbed/seed/cmd/seed -fixtures testbed/fixtures -out testbed/$(RUNTIME) -seed $(SEED) -php "$(PHP)" -only $(ID)
	$(MAKE) --no-print-directory load ID=$(ID)
	@: > $(RUNTIME)/log/sinkhole.jsonl; : > $(RUNTIME)/log/stubs.jsonl

seal: ## TestNetworkIsSealed on the running testbed
	cd $(ROOT) && ROADIE_TESTBED_REQUIRED=1 $(GO) test -tags testbed -count=1 -v ./testbed/network/

oracle: ## judge an output: make oracle ID=r01 DUMP=out.sql [TREE=dir]
	@test -n "$(ID)" -a -n "$(DUMP)$(TREE)" || { echo "usage: make oracle ID=r01 DUMP=out.sql [TREE=dir]" >&2; exit 2; }
	cd $(ROOT) && $(GO) run ./testbed/oracle/cmd/oracle -canaries testbed/$(RUNTIME)/canaries.json -scenario $(ID) \
		$(if $(DUMP),-dump $(abspath $(DUMP)) -input testbed/$(RUNTIME)/fixtures/$(ID)/db.sql -serialized testbed/$(RUNTIME)/fixtures/$(ID)/serialized.json) \
		$(if $(TREE),-tree $(abspath $(TREE))) -php "$(PHP)"

# The benchmark: every scenario of scenarios.yaml, the oracle, the drill
# and the control pass, into bench/work/results.json. rhythm-roadie is
# built from this checkout without cgo, so the same binary runs here and
# in every container. DRILL is how long the side-effect drill runs after
# each copy and prepare (5m for the published numbers).
DRILL ?= 5m
BENCH := cd $(ROOT) && $(GO) run -tags testbed ./testbed/bench -bin testbed/$(RUNTIME)/bin/rhythm-roadie -php "$(PHP)" -seed $(SEED) -drill-for $(DRILL)

roadie-bin:
	@mkdir -p $(RUNTIME)/bin
	cd $(ROOT) && CGO_ENABLED=0 GOOS=linux $(GO) build -o testbed/$(RUNTIME)/bin/rhythm-roadie ./cmd/rhythm-roadie

bench: roadie-bin ## run the benchmark (needs make up); ONLY=r01,v01 for some
	$(BENCH) $(if $(ONLY),-only $(ONLY))

reproduce: roadie-bin ## run the benchmark twice and fail if the score differs
	$(BENCH) $(if $(ONLY),-only $(ONLY)) -work testbed/bench/work/first
	$(BENCH) $(if $(ONLY),-only $(ONLY)) -work testbed/bench/work/second
	cd $(ROOT) && $(GO) run -tags testbed ./testbed/bench -diff testbed/bench/work/first/results.json testbed/bench/work/second/results.json

drill: ## the side-effect drill alone on the running staging (FOR=5m)
	cd $(ROOT) && $(GO) run -tags testbed ./testbed/bench -drill -for $(or $(FOR),5m)

status:
	$(COMPOSE) ps

logs:
	$(COMPOSE) logs -f
