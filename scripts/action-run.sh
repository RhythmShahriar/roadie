#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Runs Roadie for the GitHub Action (action.yml).
#
#   action-run.sh run     checks the inputs, runs Roadie, writes the job
#                         summary and the step outputs
#   action-run.sh split   prints ARGS split into words, one per line
#                         (what run passes on; the tests use it)
#
# run reads the Action's inputs from the environment: MODE, SOURCE, TARGET,
# URL, PRODUCTION, CONFIRM_TARGET, CONFIG, FAIL_ON, BASELINE, ALLOW_SAMPLE,
# SARIF, SARIF_UPLOAD, ARGS, UPLOAD, SUMMARY_DETAILS and PR_COMMENT, plus
# GitHub's GITHUB_EVENT_NAME and GITHUB_EVENT_PATH. It runs in the
# working-directory input, so .roadie/config.yaml is found there.
#
# Roadie writes report.json (signed), report.html and with sarif
# roadie.sarif into $RUNNER_TEMP/roadie-report, the folder the upload
# input uploads. The job summary holds counts only unless summary-details
# is "true": the full Markdown report names tables and columns and shows
# masked values. A summary is visible to everyone who can read the
# repository. Exit 1 (findings) leaves the step green so the upload and
# comment steps run; the Action's last step fails the job. Exit 2, 3 and
# 130 fail this step.
set -uo pipefail

: "${GITHUB_OUTPUT:?}" "${RUNNER_TEMP:?}"
summary=${GITHUB_STEP_SUMMARY:-/dev/null}
out="$RUNNER_TEMP/roadie-report"
markdown="$RUNNER_TEMP/roadie-summary.md"
errlog="$RUNNER_TEMP/roadie-stderr.log"
stdout_json="$RUNNER_TEMP/roadie-stdout.json"

# The PRD's exact sentences.
fork_refusal="Roadie won't copy production in a pull request from a fork: the workflow would need production credentials. Run it on push or schedule; don't use pull_request_target for this."
honesty="Pseudonymised, not anonymised. The copy still holds order totals, dates, products and anything Roadie can't recognise in free text. Treat it as personal data."

# split_args TEXT splits TEXT into the array words the way a shell splits
# a command line: blanks separate words, '...' keeps everything literally,
# "..." keeps everything but \" \\ \$ and \` and a backslash outside
# quotes keeps the next character. Nothing is ever expanded or run: $VAR,
# $(...), `...`, ~ and globs reach Roadie as written. It fails on a quote
# that isn't closed.
split_args() {
	local s=$1 n=${#1} i=0 c next word="" inword=0 quote=""
	words=()
	while [ "$i" -lt "$n" ]; do
		c=${s:i:1}
		if [ -n "$quote" ]; then
			if [ "$c" = "$quote" ]; then
				quote=""
			elif [ "$quote" = '"' ] && [ "$c" = $'\\' ] && [ $((i + 1)) -lt "$n" ]; then
				next=${s:i+1:1}
				case "$next" in
				'"' | $'\\' | '$' | '`')
					word+=$next
					i=$((i + 1))
					;;
				*) word+=$c ;;
				esac
			else
				word+=$c
			fi
		else
			case "$c" in
			' ' | $'\t' | $'\n' | $'\r')
				if [ "$inword" = 1 ]; then
					words+=("$word")
					word="" inword=0
				fi
				;;
			"'" | '"')
				quote=$c
				inword=1
				;;
			$'\\')
				inword=1
				if [ $((i + 1)) -lt "$n" ]; then
					i=$((i + 1))
					word+=${s:i:1}
				else
					word+=$c
				fi
				;;
			*)
				word+=$c
				inword=1
				;;
			esac
		fi
		i=$((i + 1))
	done
	[ -z "$quote" ] || return 1
	[ "$inword" = 0 ] || words+=("$word")
}

# fence prints a code fence longer than any run of tildes in its input, so
# text from the run can't close it and render as Markdown.
fence() {
	grep -o '~*' | awk '{ if (length($0) > n) n = length($0) } END { n = n < 3 ? 3 : n + 1; while (n--) printf "~" }'
}

# couldnt CODE TEXT tells the job summary and the log why Roadie couldn't
# run, writes the outputs it can and exits with CODE.
couldnt() {
	local code=$1 text=$2 last f
	f=$(printf '%s\n' "$text" | fence)
	{
		printf '## Roadie: ERROR\n\nRoadie could not run:\n\n%stext\n' "$f"
		printf '%s\n' "$text"
		printf '%s\n' "$f"
	} >"$markdown"
	cat "$markdown" >>"$summary"
	last=$(printf '%s\n' "$text" | sed '/^[[:space:]]*$/d' | tail -n 1)
	# Workflow commands need % escaped.
	echo "::error::Roadie could not run: ${last//%/%25}"
	{
		echo "exit-code=$code"
		echo "verdict=error"
		echo "findings=0"
		echo "summary=$markdown"
		[ -z "${roadie_version:-}" ] || echo "version=$roadie_version"
	} >>"$GITHUB_OUTPUT"
	exit "$code"
}

# masked prints its input as the runner shows it in the job log: the
# ::add-mask:: commands are gone and every value they mask, from that line
# on, reads ***. The runner unescapes %25, %0D and %0A in a value; Roadie's
# masked values never hold them.
masked() {
	awk '
		index($0, "::add-mask::") == 1 { m = substr($0, 13); if (m != "") masks[++n] = m; next }
		{
			for (i = 1; i <= n; i++)
				while ((j = index($0, masks[i])) > 0)
					$0 = substr($0, 1, j - 1) "***" substr($0, j + length(masks[i]))
			print
		}'
}

# event QUERY reads the workflow event's payload with jq; empty when
# there is none.
event() {
	[ -r "${GITHUB_EVENT_PATH:-}" ] || return 0
	jq -r "$1 // empty" "$GITHUB_EVENT_PATH" 2>/dev/null || true
}

# from_fork reports whether this run is a pull request event whose head is
# another repository than its base. A pull request event without a
# readable payload counts as one: Roadie fails closed.
from_fork() {
	case "${GITHUB_EVENT_NAME:-}" in
	pull_request | pull_request_target) ;;
	*) return 1 ;;
	esac
	[ -r "${GITHUB_EVENT_PATH:-}" ] || return 0
	local head base
	head=$(event '.pull_request.head.repo.full_name')
	base=$(event '.pull_request.base.repo.full_name')
	[ -z "$head" ] || [ -z "$base" ] || [ "$head" != "$base" ]
}

# public reports whether the payload says the repository is public.
public() {
	[ "$(event '.repository.visibility')" = public ] || [ "$(event '.repository.private | tostring')" = false ]
}

# flag NAME VALUE checks a "true"/"false" input.
flag() {
	case "$2" in
	true | false) ;;
	*) couldnt 2 "the $1 input is \"$2\": use \"true\" or \"false\"" ;;
	esac
}

# counts prints the findings line from the envelope's summary.
counts() {
	jq -r '.summary | "\(.critical // 0) critical · \(.high // 0) high · \(.medium // 0) medium · \(.low // 0) low · \(.info // 0) info"' "$1"
}

# at_or_above ENVELOPE LEVEL counts the findings at or above LEVEL. With
# none no finding fails the run, so none counts 0.
at_or_above() {
	if [ "$2" = none ]; then
		echo 0
		return
	fi
	jq -r --arg l "$2" '
		["critical", "high", "medium", "low", "info"] as $all
		| (($all | index($l)) // 4) as $i
		| [$all[0:$i + 1][] as $s | .summary[$s] // 0] | add' "$1"
}

# word ENVELOPE prints the summary's verdict word. A verify that couldn't
# read part of its input (coverage lists "(input ...)") is INCOMPLETE.
word() {
	local result kind sample unread
	result=$(jq -r '.data.result // "error"' "$1")
	kind=$(jq -r '.kind // ""' "$1")
	sample=$(jq -r '.data.coverage.sample // 0' "$1")
	unread=$(jq -r '[.data.coverage.skipped // [] | .[] | select(.table | startswith("(input"))] | length' "$1")
	case "$kind:$result" in
	plan:pass) echo "PLAN, ready" ;;
	plan:fail) echo "PLAN, copy would stop" ;;
	*:pass) if [ "$sample" != 0 ]; then echo "PASS (sample)"; else echo PASS; fi ;;
	*:fail) echo FAIL ;;
	*:error) if [ "$unread" != 0 ]; then echo INCOMPLETE; else echo ERROR; fi ;;
	*) echo ERROR ;;
	esac
}

# brief ENVELOPE writes the counts-only summary: no hosts, tables,
# columns or values, only numbers and the signer's key ID.
brief() {
	local env=$1
	{
		printf '## Roadie: %s (%s)\n\n' "$(word "$env")" "$(jq -r '.kind' "$env")"
		printf '| | |\n|---|---|\n'
		printf '| Findings | %s |\n' "$(counts "$env")"
		jq -r '
			def row(k; v): "| \(k) | \(v) |";
			.summary as $s | .data as $d
			| if .kind == "verify" then
				row("Tables checked"; "\($d.coverage.tables_scanned // 0) of \($d.coverage.tables_total // 0)"),
				row("Live keys found"; $s.keys_found // 0)
			else
				row("Tables pseudonymised"; "\($s.tables_pseudonymised // 0) of \($s.tables // 0)"),
				row("Values pseudonymised"; $s.values_pseudonymised // 0),
				row("Live keys"; "\($s.keys_found // 0) found, \($s.keys_swapped // 0) swapped, \($s.keys_blanked // 0) blanked"),
				row("Side effects off"; "\($s.side_effects_off // 0) of \($s.side_effects_total // 0)"),
				row("Uncovered columns"; $s.uncovered_columns // 0)
			end,
			(if $d.signature.key_id then row("Signed by"; "`\($d.signature.key_id)`") else empty end)' "$env"
		local msg
		msg=$(jq -r '.data.error.message // empty' "$env")
		if [ -n "$msg" ]; then
			local f
			f=$(printf '%s\n' "$msg" | fence)
			printf '\nRoadie stopped:\n\n%stext\n%s\n%s\n' "$f" "$msg" "$f"
		fi
		printf '\n%s\n\n' "$honesty"
		printf '_Counts only. Set summary-details: "true" for the full report here, or upload: "true" for the HTML report as an artifact._\n'
	} >"$markdown"
}

# artifact_name prints the report artifact's name: roadie-report for the
# first use of the Action in a job, then roadie-report-2, -3 and so on,
# since artifact names are unique in a workflow run.
artifact_name() {
	local count="$RUNNER_TEMP/roadie-artifacts" n=0
	[ ! -f "$count" ] || n=$(cat "$count")
	[[ $n =~ ^[0-9]+$ ]] || n=0
	n=$((n + 1))
	echo "$n" >"$count"
	if [ "$n" = 1 ]; then echo roadie-report; else echo "roadie-report-$n"; fi
}

run() {
	roadie_version=$(rhythm-roadie version --json 2>/dev/null | jq -r '.version // empty' 2>/dev/null || true)

	local mode=${MODE:-verify}
	case "$mode" in
	verify | copy | prepare | plan) ;;
	*) couldnt 2 "unknown mode \"$mode\": use verify, copy, prepare or plan" ;;
	esac
	local allow_sample=${ALLOW_SAMPLE:-false} sarif=${SARIF:-false} sarif_upload=${SARIF_UPLOAD:-false}
	local upload=${UPLOAD:-false} details=${SUMMARY_DETAILS:-false} comment=${PR_COMMENT:-false}
	flag allow-sample "$allow_sample"
	flag sarif "$sarif"
	flag sarif-upload "$sarif_upload"
	flag upload "$upload"
	flag summary-details "$details"
	flag pr-comment "$comment"

	# A pull request from a fork runs code nobody reviewed yet; copy and
	# prepare there would hand it production's credentials.
	if [ "$mode" = copy ] || [ "$mode" = prepare ]; then
		from_fork && couldnt 2 "$fork_refusal"
	fi

	local source=${SOURCE:-} target=${TARGET:-}
	case "$mode" in
	copy) if [ -z "$source" ] || [ -z "$target" ]; then couldnt 2 "mode copy needs the source and target inputs"; fi ;;
	prepare) [ -n "$target" ] || couldnt 2 "mode prepare needs the target input: the staging site to make safe" ;;
	plan) [ -n "$source" ] || [ -n "$target" ] || couldnt 2 "mode plan needs the source input (a copy's plan) or the target input (a prepare's plan)" ;;
	esac
	[ -z "${CONFIRM_TARGET:-}" ] || [ "$mode" = copy ] || [ "$mode" = prepare ] ||
		couldnt 2 "the confirm-target input works with mode copy or prepare, not $mode"
	[ -z "${BASELINE:-}" ] || [ "$mode" = verify ] ||
		couldnt 2 "the baseline input works with mode verify, not $mode"
	[ "$sarif" = false ] || [ "$mode" = verify ] ||
		couldnt 2 "the sarif input works with mode verify, not $mode"
	[ "$sarif_upload" = false ] || [ "$sarif" = true ] ||
		couldnt 2 "the sarif-upload input needs sarif: \"true\""

	if public; then
		[ "$upload" = false ] ||
			echo "::warning::upload is on in a public repository: anyone who can read it can download the roadie-report artifact, which names your tables and columns and shows masked values. Turn upload off or keep the repository private."
		[ "$sarif_upload" = false ] ||
			echo "::warning::sarif-upload is on in a public repository: its code scanning alerts name your tables and columns. Check who can see them before you keep it on."
	fi

	split_args "${ARGS:-}" || couldnt 2 "the args input has a quote that isn't closed"
	# ${a[@]+...}: an empty array under set -u, even in macOS's bash 3.2.
	local extra=(${words[@]+"${words[@]}"})

	local cmd=(rhythm-roadie "$mode")
	case "$mode" in
	verify) [ -z "$target" ] || cmd+=("$target") ;;
	copy) cmd+=(--from "$source" --to "$target") ;;
	prepare) cmd+=("$target") ;;
	plan)
		if [ -n "$source" ]; then
			cmd+=(--from "$source")
			[ -z "$target" ] || cmd+=(--to "$target")
		else
			cmd+=("$target")
		fi
		;;
	esac
	if [ -n "${URL:-}" ]; then
		# verify fetches the copy's robots.txt and headers with --site;
		# the others rewrite production's URLs to it with --url.
		if [ "$mode" = verify ]; then cmd+=(--site "$URL"); else cmd+=(--url "$URL"); fi
	fi
	[ -z "${PRODUCTION:-}" ] || cmd+=(--production "$PRODUCTION")
	[ -z "${CONFIRM_TARGET:-}" ] || cmd+=(--confirm-target "$CONFIRM_TARGET")
	[ -z "${CONFIG:-}" ] || cmd+=(--config "$CONFIG")
	[ -z "${BASELINE:-}" ] || cmd+=(--baseline "$BASELINE")
	cmd+=(--fail-on "${FAIL_ON:-high}")

	rm -rf "$out"
	mkdir -p "$out"
	local json="$out/report.json" html="$out/report.html" sarif_file=""
	cmd+=(--json -o "$json" -o "$html")
	if [ "$sarif" = true ]; then
		sarif_file="$out/roadie.sarif"
		cmd+=(-o "$sarif_file")
	fi

	# stdout is the JSON report, kept out of the log; stderr goes to the
	# log as it happens and to a file for the summary.
	{ "${cmd[@]}" ${extra[@]+"${extra[@]}"} 2>&1 1>&3 3>&- | tee "$errlog" >&2; } 3>"$stdout_json"
	local code=$?
	# Roadie masks a secret with ::add-mask:: before it prints anything
	# that holds it (the --basic-auth password). The runner hides the value
	# in the job log; the copy kept for the summary gets the same.
	if grep -q '^::add-mask::' "$errlog"; then
		masked <"$errlog" >"$errlog.tmp"
		mv "$errlog.tmp" "$errlog"
	fi

	local env=""
	if [ -s "$json" ] && jq -e .schema "$json" >/dev/null 2>&1; then
		env=$json
	elif [ -s "$stdout_json" ] && jq -e .schema "$stdout_json" >/dev/null 2>&1; then
		env=$stdout_json
	fi
	if [ -z "$env" ]; then
		local lines
		lines=$(sed '/^[[:space:]]*$/d' "$errlog" | tail -n 5)
		[ "$code" -ge 2 ] || code=3
		couldnt "$code" "${lines:-rhythm-roadie exited with $code and wrote no report}"
	fi

	local verdict
	verdict=$(jq -r '.data.result // empty' "$env")
	if [ -z "$verdict" ]; then
		case "$code" in 0) verdict=pass ;; 1) verdict=fail ;; *) verdict=error ;; esac
	fi
	# A sampled verify skipped rows, so it can't stand for a full check.
	if [ "$code" = 0 ] && [ "$allow_sample" = false ] && [ "$(jq -r '.data.coverage.sample // 0' "$env")" != 0 ]; then
		echo "::error::Roadie checked a sample of each table, not every row, so this run counts as failed. Drop --sample, or set allow-sample: \"true\" to accept a sampled verify."
		code=1 verdict=fail
	fi

	if [ "$details" = true ] && rhythm-roadie report "$json" --md >"$markdown" 2>>"$errlog" && [ -s "$markdown" ]; then
		:
	else
		brief "$env"
	fi
	cat "$markdown" >>"$summary"

	local findings
	findings=$(at_or_above "$env" "${FAIL_ON:-high}")
	echo "Roadie: $(word "$env"), $(counts "$env") (exit $code)"
	{
		echo "exit-code=$code"
		echo "verdict=$verdict"
		echo "findings=${findings:-0}"
		echo "summary=$markdown"
		echo "artifact=$(artifact_name)"
		[ ! -s "$json" ] || echo "report=$json"
		[ ! -s "$html" ] || echo "html=$html"
		[ -z "$sarif_file" ] || [ ! -s "$sarif_file" ] || echo "sarif-file=$sarif_file"
		echo "signature=$(jq -r '.data.signature.key_id // empty' "$env")"
		[ -z "$roadie_version" ] || echo "version=$roadie_version"
	} >>"$GITHUB_OUTPUT"
	[ "$code" -lt 2 ] || exit "$code"
	exit 0
}

case "${1:-}" in
run) run ;;
split)
	split_args "${ARGS:-}" || {
		echo "the args input has a quote that isn't closed" >&2
		exit 2
	}
	[ ${#words[@]} -eq 0 ] || printf '%s\n' "${words[@]}"
	;;
*)
	echo "usage: action-run.sh run|split" >&2
	exit 2
	;;
esac
