# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

GOLANGCI := go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0

# Every fuzz target as package:FuzzName (PRD 22.4). A target whose package
# doesn't have it yet is skipped with a note, so the list can name targets
# before their lane lands them.
FUZZ_TARGETS := \
	dump/mysql:FuzzTokenize \
	dump/mysql:FuzzRoundTrip \
	dump/mysql:FuzzSQLStringEscape \
	dump/allow:FuzzAllow \
	dump/pgsql:FuzzCopy \
	codec/phpser:FuzzPHPSerializedRoundTrip \
	codec/phpser:FuzzPHPSerializedRewrite \
	codec/phpser:FuzzDifferentialPHP \
	codec/phpser:FuzzRewriteScalars \
	codec/nested:FuzzNested \
	codec/urlrw:FuzzURLRewrite \
	pseudo:FuzzScrub \
	site/wpconfig:FuzzWPConfigEdit \
	site/dotenv:FuzzDotenv \
	site:FuzzEndpoint \
	detect:FuzzEmail \
	detect:FuzzPhone \
	report:FuzzCanonical \
	db:FuzzMask \
	db:FuzzObjectName \
	detect:FuzzSurvivors \
	dump/allow:FuzzSetGlobal \
	dump:FuzzSniff \
	dump/mysql:FuzzSafeExpr \
	dump/mysql:FuzzSQLMode \
	dump/pgsql:FuzzCopyField \
	dump/pgsql:FuzzSQLString \
	dump/pgsql:FuzzStatements \
	engine:FuzzSerializedStrings \
	keys:FuzzSettings \
	keys:FuzzTokenSkip \
	profile:FuzzChecksumOf \
	profile:FuzzExtract \
	profile:FuzzFieldTypes \
	profile:FuzzParse \
	roadiecfg:FuzzLoad \
	roadiecfg:FuzzParseRule \
	roadiecfg:FuzzStripPassword \
	scripts/screenshot:FuzzRender \
	site/exposure:FuzzBlocks \
	site:FuzzParseFind \
	site:FuzzParseLs \
	site:FuzzSplitWPHost \
	site/remote:FuzzAliasSSH \
	testbed/bench:FuzzSplitRows \
	testbed/gen:FuzzParseSize \
	testbed/oracle:FuzzReadDump \
	testbed/oracle:FuzzScanFile \
	testbed/sinkhole:FuzzParseQuery \
	verify:FuzzLooksSerialized \
	verify:FuzzMetaNoindex \
	verify:FuzzPHPArrayValue \
	verify:FuzzRobots

# How long each target runs: 5 s for the CI smoke, FUZZTIME for make fuzz.
FUZZTIME ?= 5m

.PHONY: build test lint fmt check hooks golden fuzz-smoke fuzz no-network testbed testbed-down bench reproduce actionlint

build: ## build bin/rhythm-roadie
	go build -o bin/rhythm-roadie ./cmd/rhythm-roadie

test:
	go test -race ./...

lint:
	$(GOLANGCI) run ./...

fmt:
	gofmt -w .

check: lint test ## everything CI runs on every OS

hooks: ## enable the Conventional Commits hook
	git config core.hooksPath .githooks

# ROADIE_UPDATE=1 is the -update flag for every package at once: a package
# whose tests don't import testutil would reject the flag itself.
golden: ## rewrite the golden files from the current output
	ROADIE_UPDATE=1 go test -count=1 ./...

fuzz-smoke: ## every fuzz target for 5 s
	@$(MAKE) --no-print-directory fuzz FUZZTIME=5s

fuzz: ## every fuzz target for FUZZTIME (default 5m)
	@set -e; for t in $(FUZZ_TARGETS); do \
		pkg=./$${t%%:*}; name=$${t#*:}; \
		if [ ! -d "$$pkg" ] || ! go test -list "^$$name\$$" "$$pkg" | grep -qx "$$name"; then \
			echo "skip $$pkg $$name: not in the package yet"; continue; \
		fi; \
		echo "fuzz $$pkg $$name for $(FUZZTIME)"; \
		go test -run '^$$' -fuzz "^$$name\$$" -fuzztime $(FUZZTIME) "$$pkg"; \
	done

no-network: ## copy and verify with no network at all (Linux, needs sudo)
	sudo unshare --net -- sudo -E -u "$$USER" env PATH="$$PATH" go test -tags nonet -run TestNoNetwork ./cmd/rhythm-roadie/

testbed: ## start the sealed Docker testbed
	$(MAKE) -C testbed up

testbed-down:
	$(MAKE) -C testbed down

bench: build ## run the benchmark against the testbed; writes docs/benchmark.md
	go run -tags testbed ./testbed/bench -bin $(CURDIR)/bin/rhythm-roadie

reproduce: ## run the testbed scenarios from a clean start
	$(MAKE) -C testbed reproduce

actionlint: ## lint the GitHub workflows, from the image CI pins
	docker run --rm -v "$(CURDIR):/repo" -w /repo rhysd/actionlint@sha256:887a259a5a534f3c4f36cb02dca341673c6089431057242cdc931e9f133147e9 -color
