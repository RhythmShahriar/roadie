#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Tests the Action's helper scripts offline: action-run.sh with a fake
# rhythm-roadie that records its arguments, action-ssh.sh in a fake home,
# action-install.sh with a fake curl, verify-release.sh with a fake
# cosign and action-comment.sh's body. Event payloads come from
# scripts/testdata. CI runs it; so can you: scripts/action-test.sh
#
# The checks run conditions as bash -c '...' _ ARGS: the dollars in them
# are the inner shell's positional parameters, meant to stay literal here.
# shellcheck disable=SC2016
set -uo pipefail

here=$(cd "$(dirname "$0")" && pwd)
fixtures="$here/testdata"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin"

# The fake Roadie: arguments to $FAKE_ARGS (one per line), stderr from
# FAKE_STDERR, exit code from FAKE_EXIT. Each -o file gets FAKE_REPORT
# (.json) or a stand-in; stdout gets FAKE_REPORT too, as --json would.
cat >"$work/bin/rhythm-roadie" <<'FAKE'
#!/usr/bin/env bash
case "${1:-}" in
version) printf '{"schema":"rhythm-version/v1","name":"rhythm-roadie","version":"9.8.7","commit":"","kit_version":"v0.8.0"}\n'; exit 0 ;;
--version) echo "rhythm-roadie 9.8.7"; exit 0 ;;
report) printf '## Roadie: FAIL, staging.sql is not a safe copy yet\n\n- wp_users_canary_table.user_email\n'; exit 0 ;;
esac
printf '%s\n' "$@" >"$FAKE_ARGS"
while [ $# -gt 0 ]; do
  if [ "$1" = -o ] && [ -n "${FAKE_REPORT:-}" ]; then
    case "$2" in
    *.json) cp "$FAKE_REPORT" "$2" ;;
    *.html) echo '<!doctype html><title>report</title>' >"$2" ;;
    *.sarif) echo '{"version":"2.1.0","runs":[]}' >"$2" ;;
    esac
  fi
  shift
done
[ -z "${FAKE_REPORT:-}" ] || cat "$FAKE_REPORT"
[ -z "${FAKE_STDERR:-}" ] || printf '%s\n' "$FAKE_STDERR" >&2
exit "${FAKE_EXIT:-0}"
FAKE
# The fake curl answers the latest-release lookup.
cat >"$work/bin/curl" <<'FAKE'
#!/usr/bin/env bash
printf 'https://github.com/RhythmShahriar/roadie/releases/tag/v9.8.7'
FAKE
# The fake cosign passes unless FAKE_COSIGN_FAIL is set and records its
# arguments.
cat >"$work/bin/cosign" <<'FAKE'
#!/usr/bin/env bash
printf '%s\n' "$@" >"$FAKE_COSIGN_ARGS"
[ -z "${FAKE_COSIGN_FAIL:-}" ]
FAKE
chmod +x "$work/bin/rhythm-roadie" "$work/bin/curl" "$work/bin/cosign"
export PATH="$work/bin:$PATH" FAKE_ARGS="$work/args" FAKE_COSIGN_ARGS="$work/cosign-args"

fail=0
ok() { echo "ok   $1"; }
bad() {
	echo "FAIL $1"
	sed 's/^/     /' "$work/log"
	fail=1
}
check() { # check NAME CONDITION... runs the condition and reports it
	local name=$1
	shift
	if "$@"; then ok "$name"; else bad "$name"; fi
}

# act SCRIPT ARG [VAR=value...] runs a helper with a fresh runner
# environment and the given inputs. Its status is in $code.
act() {
	local script=$1 arg=$2
	shift 2
	rm -rf "$work/runner" "$work/args"
	mkdir -p "$work/runner/temp" "$work/site"
	: >"$work/runner/output"
	: >"$work/runner/summary"
	: >"$work/runner/path"
	(
		cd "$work/site" || exit 99
		env GITHUB_OUTPUT="$work/runner/output" GITHUB_STEP_SUMMARY="$work/runner/summary" \
			GITHUB_PATH="$work/runner/path" RUNNER_TEMP="$work/runner/temp" GITHUB_EVENT_NAME=push \
			GITHUB_EVENT_PATH="$fixtures/event-push-private.json" "$@" \
			"$here/$script" "$arg"
	) >"$work/log" 2>&1
	code=$?
}
has() { grep -qxF -- "$2" "$1"; }        # has FILE LINE
contains() { grep -qF -- "$2" "$1"; }    # contains FILE TEXT
lacks() { ! grep -qF -- "$2" "$1"; }     # lacks FILE TEXT
ran() { [ -e "$work/args" ]; }           # Roadie was started
out() { has "$work/runner/output" "$1"; } # out NAME=VALUE

# --- args: split like a shell, never evaluated ----------------------------

# split ARGS prints what action-run.sh split makes of ARGS, one per line.
split() {
	ARGS=$1 RUNNER_TEMP="$work" GITHUB_OUTPUT=/dev/null "$here/action-run.sh" split
}
want=$'--yes\n--keep-users\nadmin,*@agency.example\na "b" c\nback slash\n$HOME\n$(id)\n`id`\n~\n/*\nit\'s\n\\x\n\nend'
got=$(split $'--yes  --keep-users \'admin,*@agency.example\'\t"a \\"b\\" c" back\\ slash $HOME $(id) `id` ~ /* it"\'"s "\\\\x" \'\' end')
if [ "$got" = "$want" ]; then ok "args split like a shell, nothing expanded"; else
	printf 'got:\n%s\nwant:\n%s\n' "$got" "$want" >"$work/log"
	bad "args split like a shell, nothing expanded"
fi
got=$(split $'"double \\$keeps\\` and \\n stays" \'single \\" stays\'')
if [ "$got" = $'double $keeps` and \\n stays\nsingle \\" stays' ]; then ok "backslashes inside quotes"; else
	printf 'got:\n%s\n' "$got" >"$work/log"
	bad "backslashes inside quotes"
fi
check "no args, no words" [ -z "$(split '   ')" ]
for unclosed in "'open" '"open' "a 'b\" c"; do
	if split "$unclosed" >"$work/log" 2>&1; then bad "an unclosed quote is refused: $unclosed"; else ok "an unclosed quote is refused: $unclosed"; fi
done

act action-run.sh run MODE=verify TARGET=staging.sql ARGS="--sample 'not a number' *" FAKE_REPORT="$fixtures/report-verify-fail.json"
check "args reach Roadie as split words, after the Action's own" \
	bash -c '[ "$(tail -n 3 "$1")" = "$(printf "%s\n" --sample "not a number" "*")" ]' _ "$work/args"

act action-run.sh run MODE=verify ARGS="--skip 'names"
check "an unclosed quote in args stops before Roadie runs" bash -c '[ "$1" = 2 ] && ! [ -e "$2" ]' _ "$code" "$work/args"
contains "$work/runner/summary" "the args input has a quote that isn't closed" || bad "the unclosed-quote message reaches the summary"

# --- the fork refusal --------------------------------------------------------

msg="Roadie won't copy production in a pull request from a fork: the workflow would need production credentials. Run it on push or schedule; don't use pull_request_target for this."
for event in pull_request pull_request_target; do
	for mode in copy prepare; do
		act action-run.sh run MODE=$mode SOURCE=prod.sql TARGET=/var/www/staging GITHUB_EVENT_NAME=$event \
			GITHUB_EVENT_PATH="$fixtures/event-pr-fork.json" FAKE_REPORT="$fixtures/report-copy-pass.json"
		if [ "$code" = 2 ] && ! ran && contains "$work/runner/summary" "$msg" && contains "$work/log" "::error::Roadie could not run: $msg" &&
			out exit-code=2 && out verdict=error; then
			ok "$event from a fork: $mode is refused"
		else bad "$event from a fork: $mode is refused"; fi
	done
done
act action-run.sh run MODE=copy SOURCE=prod.sql TARGET=out.sql GITHUB_EVENT_NAME=pull_request \
	GITHUB_EVENT_PATH="$fixtures/event-pr-deleted-fork.json"
check "a pull request whose fork is gone is refused" bash -c '[ "$1" = 2 ] && ! [ -e "$2" ]' _ "$code" "$work/args"
act action-run.sh run MODE=copy SOURCE=prod.sql TARGET=out.sql GITHUB_EVENT_NAME=pull_request GITHUB_EVENT_PATH="$work/missing.json"
check "a pull request without a payload is refused (fail closed)" bash -c '[ "$1" = 2 ] && ! [ -e "$2" ]' _ "$code" "$work/args"
act action-run.sh run MODE=copy SOURCE=prod.sql TARGET=out.sql GITHUB_EVENT_NAME=pull_request \
	GITHUB_EVENT_PATH="$fixtures/event-pr-same.json" FAKE_REPORT="$fixtures/report-copy-pass.json"
check "a pull request from the same repository may copy" bash -c '[ "$1" = 0 ] && [ -e "$2" ]' _ "$code" "$work/args"
act action-run.sh run MODE=verify TARGET=staging.sql GITHUB_EVENT_NAME=pull_request \
	GITHUB_EVENT_PATH="$fixtures/event-pr-fork.json" FAKE_REPORT="$fixtures/report-verify-fail.json"
check "verify runs in a pull request from a fork" bash -c '[ "$1" = 0 ] && [ -e "$2" ]' _ "$code" "$work/args"
act action-run.sh run MODE=plan SOURCE=prod.sql GITHUB_EVENT_NAME=pull_request \
	GITHUB_EVENT_PATH="$fixtures/event-pr-fork.json" FAKE_REPORT="$fixtures/report-copy-pass.json"
check "plan runs in a pull request from a fork" bash -c '[ "$1" = 0 ] && [ -e "$2" ]' _ "$code" "$work/args"

# --- public repositories -------------------------------------------------------

act action-run.sh run MODE=verify UPLOAD=true SARIF=true SARIF_UPLOAD=true GITHUB_EVENT_PATH="$fixtures/event-push-public.json" \
	FAKE_REPORT="$fixtures/report-verify-fail.json"
check "upload in a public repository warns" contains "$work/log" "::warning::upload is on in a public repository"
check "sarif-upload in a public repository warns" contains "$work/log" "::warning::sarif-upload is on in a public repository"
act action-run.sh run MODE=verify UPLOAD=true SARIF=true SARIF_UPLOAD=true FAKE_REPORT="$fixtures/report-verify-fail.json"
check "a private repository gets no warning" lacks "$work/log" "::warning::"
act action-run.sh run MODE=verify GITHUB_EVENT_PATH="$fixtures/event-push-public.json" FAKE_REPORT="$fixtures/report-verify-fail.json"
check "no upload, no warning" lacks "$work/log" "::warning::"

# --- a run and its outputs -------------------------------------------------------

act action-run.sh run MODE=verify TARGET=staging.sql URL=https://staging.shop.test PRODUCTION=shop.com,*.shop.com SARIF=true \
	BASELINE=accepted.json CONFIG=".roadie/ci config.yaml" FAKE_EXIT=1 FAKE_REPORT="$fixtures/report-verify-fail.json"
t="$work/runner/temp/roadie-report"
if [ "$code" = 0 ] && out exit-code=1 && out verdict=fail && out findings=2 && out "report=$t/report.json" && out "html=$t/report.html" &&
	out "sarif-file=$t/roadie.sarif" && out signature=roadie:7Q3kXc2bW9pLmN4a && out version=9.8.7 && out artifact=roadie-report; then
	ok "findings: the step passes and the outputs say exit 1, fail, 2 at or above high"
else bad "findings: the step passes and the outputs say exit 1, fail, 2 at or above high"; fi
want=$(printf '%s\n' verify staging.sql --site https://staging.shop.test --production 'shop.com,*.shop.com' --config ".roadie/ci config.yaml" \
	--baseline accepted.json --fail-on high --json -o "$t/report.json" -o "$t/report.html" -o "$t/roadie.sarif")
check "verify's command line" bash -c '[ "$(cat "$1")" = "$2" ]' _ "$work/args" "$want"
if contains "$work/runner/summary" "## Roadie: FAIL (verify)" && contains "$work/runner/summary" "| Findings | 1 critical · 1 high · 3 medium · 0 low · 2 info |" &&
	contains "$work/runner/summary" '| Signed by | `roadie:7Q3kXc2bW9pLmN4a` |' &&
	contains "$work/runner/summary" "Pseudonymised, not anonymised." &&
	lacks "$work/runner/summary" wp_users_canary_table && lacks "$work/runner/summary" "staging.sql" && lacks "$work/runner/summary" "a•••"; then
	ok "the summary holds counts only: no tables, hosts or values"
else
	cat "$work/runner/summary" >>"$work/log"
	bad "the summary holds counts only: no tables, hosts or values"
fi
check "the log has the result line" contains "$work/log" "Roadie: FAIL, 1 critical · 1 high · 3 medium · 0 low · 2 info (exit 1)"
check "the JSON on stdout stays out of the log" lacks "$work/log" '"schema"'

act action-run.sh run MODE=verify FAIL_ON=medium SUMMARY_DETAILS=true FAKE_EXIT=1 FAKE_REPORT="$fixtures/report-verify-fail.json"
check "fail-on medium counts medium and up" out findings=5
check "summary-details puts the full report in the summary" contains "$work/runner/summary" "wp_users_canary_table.user_email"
act action-run.sh run MODE=verify FAIL_ON=none FAKE_REPORT="$fixtures/report-verify-fail.json"
check "fail-on none counts 0: nothing fails the run" out findings=0

# --- the --basic-auth password stays out of the job log --------------------
# runner_view FILE prints FILE as the runner shows it in the job log: the
# ::add-mask:: commands are gone and every value they mask, from that
# line on, reads ***. It models the runner on its own, apart from
# action-run.sh's masked.
runner_view() {
	awk '
		index($0, "::add-mask::") == 1 { masks[++n] = substr($0, 13); next }
		{ for (i = 1; i <= n; i++) while ((j = index($0, masks[i])) > 0) $0 = substr($0, 1, j - 1) "***" substr($0, j + length(masks[i])); print }
	' "$1"
}
pw="Qx7-pw-$(printf 'staging')-only"
auth_line="  Basic auth for team: password in .roadie/basic-auth/password (0600; the job log masks it)"
act action-run.sh run MODE=copy SOURCE=prod.sql TARGET=out.sql ARGS="--basic-auth team" \
	FAKE_REPORT="$fixtures/report-copy-pass.json" FAKE_STDERR=$'::add-mask::'"$pw"$'\n'"$auth_line"$'\n  (debug echo: '"$pw"')'
runner_view "$work/log" >"$work/log.masked"
if [ "$code" = 0 ] && contains "$work/log.masked" "$auth_line" && contains "$work/log.masked" "(debug echo: ***)" &&
	lacks "$work/log.masked" "$pw" && lacks "$work/runner/summary" "$pw" && lacks "$work/runner/output" "$pw" &&
	lacks "$work/runner/temp/roadie-stderr.log" "$pw"; then
	ok "the basic-auth password is masked in the log and kept out of the summary"
else bad "the basic-auth password is masked in the log and kept out of the summary"; fi
act action-run.sh run MODE=copy SOURCE=prod.sql TARGET=out.sql ARGS="--basic-auth team" FAKE_EXIT=3 \
	FAKE_STDERR=$'::add-mask::'"$pw"$'\nrhythm roadie copy: the target went away'
check "a failed run's summary never shows the mask command" bash -c '! grep -qF -- "$1" "$2" && grep -qF "the target went away" "$2"' _ "$pw" "$work/runner/summary"

act action-run.sh run MODE=copy SOURCE=ssh://deploy@shop.com/var/www/shop TARGET=out.sql URL=https://staging.shop.test PRODUCTION=shop.com \
	CONFIRM_TARGET=staging.shop.test FAKE_REPORT="$fixtures/report-copy-pass.json"
want=$(printf '%s\n' copy --from ssh://deploy@shop.com/var/www/shop --to out.sql --url https://staging.shop.test --production shop.com \
	--confirm-target staging.shop.test --fail-on high --json -o "$t/report.json" -o "$t/report.html")
check "copy's command line" bash -c '[ "$(cat "$1")" = "$2" ]' _ "$work/args" "$want"
if [ "$code" = 0 ] && out exit-code=0 && out verdict=pass && out findings=0 && contains "$work/runner/summary" "## Roadie: PASS (copy)" &&
	contains "$work/runner/summary" "| Tables pseudonymised | 12 of 40 |" && contains "$work/runner/summary" "| Live keys | 2 found, 1 swapped, 1 blanked |" &&
	contains "$work/runner/summary" "| Side effects off | 6 of 6 |"; then
	ok "a copy that passes"
else bad "a copy that passes"; fi

# --no-verify: written, nothing checked. Never PASS, also in a report
# that says pass with verify_mode none.
for r in report-copy-unchecked.json report-copy-nocheck-old.json; do
	act action-run.sh run MODE=copy SOURCE=prod.sql TARGET=out.sql FAKE_REPORT="$fixtures/$r"
	if [ "$code" = 0 ] && out verdict=unchecked && contains "$work/runner/summary" "## Roadie: DONE, not checked (copy)" &&
		lacks "$work/runner/summary" "PASS"; then
		ok "an unchecked copy is DONE, not PASS ($r)"
	else bad "an unchecked copy is DONE, not PASS ($r)"; fi
done

act action-run.sh run MODE=prepare TARGET=/var/www/staging FAKE_REPORT="$fixtures/report-copy-pass.json"
check "prepare takes the target as its argument" bash -c '[ "$(sed -n 2p "$1")" = /var/www/staging ]' _ "$work/args"
act action-run.sh run MODE=plan TARGET=/var/www/staging FAKE_REPORT="$fixtures/report-copy-pass.json"
check "plan without a source plans a prepare" bash -c '[ "$(sed -n 1,2p "$1")" = "$(printf "plan\n/var/www/staging")" ]' _ "$work/args"
act action-run.sh run MODE=plan SOURCE=prod.sql TARGET=out.sql FAKE_REPORT="$fixtures/report-copy-pass.json"
check "plan with a source plans a copy" bash -c '[ "$(sed -n 1,5p "$1")" = "$(printf "plan\n--from\nprod.sql\n--to\nout.sql")" ]' _ "$work/args"

# A sampled verify skipped rows: a failure unless allow-sample.
act action-run.sh run MODE=verify FAKE_REPORT="$fixtures/report-verify-sample.json"
if out exit-code=1 && out verdict=fail && contains "$work/log" "::error::Roadie checked a sample of each table" &&
	contains "$work/runner/summary" "## Roadie: PASS (sample) (verify)"; then
	ok "a sampled verify counts as failed"
else bad "a sampled verify counts as failed"; fi
act action-run.sh run MODE=verify ALLOW_SAMPLE=true FAKE_REPORT="$fixtures/report-verify-sample.json"
check "allow-sample accepts it" bash -c 'grep -qx exit-code=0 "$1" && grep -qx verdict=pass "$1"' _ "$work/runner/output"

# A verify that couldn't read part of its input: exit 3, INCOMPLETE.
act action-run.sh run MODE=verify FAKE_EXIT=3 FAKE_REPORT="$fixtures/report-verify-incomplete.json"
if [ "$code" = 3 ] && out exit-code=3 && out verdict=error && contains "$work/runner/summary" "## Roadie: INCOMPLETE (verify)"; then
	ok "a verify that read part of its input is incomplete"
else bad "a verify that read part of its input is incomplete"; fi

# Roadie stopped: its message, or its last lines without a report.
act action-run.sh run MODE=copy SOURCE=prod.sql TARGET=/var/www/shop FAKE_EXIT=3 FAKE_REPORT="$fixtures/report-copy-error.json"
if [ "$code" = 3 ] && out exit-code=3 && out verdict=error && contains "$work/runner/summary" "Roadie stopped:" &&
	contains "$work/runner/summary" "target-is-production: /var/www/shop"; then
	ok "exit 3 with a report: the step fails and the summary says why"
else bad "exit 3 with a report: the step fails and the summary says why"; fi
act action-run.sh run MODE=verify FAKE_EXIT=3 FAKE_STDERR=$'reading staging.sql\n\nrhythm roadie verify: staging.sql: no such file'
if [ "$code" = 3 ] && out exit-code=3 && out verdict=error && contains "$work/runner/summary" "Roadie could not run:" &&
	contains "$work/runner/summary" "staging.sql: no such file" && contains "$work/log" "::error::Roadie could not run: rhythm roadie verify: staging.sql: no such file"; then
	ok "exit 3 without a report: the summary has Roadie's last lines"
else bad "exit 3 without a report: the summary has Roadie's last lines"; fi
act action-run.sh run MODE=verify FAKE_EXIT=3 FAKE_STDERR=$'page said:\n~~~\n[click](https://evil.example)'
md="$work/runner/temp/roadie-summary.md"
check "text from the run can't end the code block" bash -c '[ "$(tail -n 1 "$1")" = "~~~~" ] && [ "$(grep -cx "~~~~" "$1")" = 1 ]' _ "$md"

# Inputs that don't fit the mode.
while IFS='|' read -r name vars text; do
	# shellcheck disable=SC2086 # vars holds several VAR=value words
	act action-run.sh run $vars
	if [ "$code" = 2 ] && ! ran && contains "$work/runner/summary" "$text"; then ok "input error: $name"; else bad "input error: $name"; fi
done <<'CASES'
unknown mode|MODE=restore|unknown mode "restore": use verify, copy, prepare or plan
copy without a target|MODE=copy SOURCE=prod.sql|mode copy needs the source and target inputs
prepare without a target|MODE=prepare|mode prepare needs the target input
plan without either|MODE=plan|mode plan needs the source input
confirm-target on verify|MODE=verify CONFIRM_TARGET=x|the confirm-target input works with mode copy or prepare, not verify
baseline on copy|MODE=copy SOURCE=a.sql TARGET=b.sql BASELINE=r.json|the baseline input works with mode verify, not copy
sarif on plan|MODE=plan SOURCE=a.sql SARIF=true|the sarif input works with mode verify, not plan
sarif-upload without sarif|MODE=verify SARIF_UPLOAD=true|the sarif-upload input needs sarif: "true"
a flag that isn't true or false|MODE=verify UPLOAD=yes|the upload input is "yes": use "true" or "false"
CASES

# Each use of the Action in a job uploads under its own name.
rm -rf "$work/runner" && mkdir -p "$work/runner/temp" && : >"$work/runner/output"
for _ in 1 2 3; do
	(cd "$work/site" && GITHUB_OUTPUT="$work/runner/output" GITHUB_STEP_SUMMARY=/dev/null RUNNER_TEMP="$work/runner/temp" \
		FAKE_REPORT="$fixtures/report-verify-fail.json" "$here/action-run.sh" run) >"$work/log" 2>&1
done
check "each use of the Action in a job gets its own artifact name" \
	bash -c '[ "$(grep "^artifact=" "$1" | tr "\n" " ")" = "artifact=roadie-report artifact=roadie-report-2 artifact=roadie-report-3 " ]' _ "$work/runner/output"

# --- the SSH key -----------------------------------------------------------------

# ssh ARG [VAR=value...] runs action-ssh.sh with HOME in the work folder.
ssh_act() {
	local arg=$1
	shift
	env HOME="$work/home" RUNNER_TEMP="$work/runner/temp" "$@" "$here/action-ssh.sh" "$arg" >"$work/log" 2>&1
	code=$?
}
key=$'-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAAfakefakefake\n-----END OPENSSH PRIVATE KEY-----'
rm -rf "${work:?}/home" "${work:?}/runner" && mkdir -p "$work/home/.ssh" "$work/runner/temp"
printf 'Host old\n  User someone\n' >"$work/home/.ssh/config"
printf 'old.example ssh-ed25519 AAAAold\n' >"$work/home/.ssh/known_hosts"
cp "$work/home/.ssh/config" "$work/config.before"
cp "$work/home/.ssh/known_hosts" "$work/known_hosts.before"

ssh_act setup SSH_KEY="$key"
check "ssh-key without known-hosts is an input error" bash -c '[ "$1" = 2 ] && ! [ -e "$2" ]' _ "$code" "$work/home/.ssh/roadie_action_key"
check "it says why" contains "$work/log" "the ssh-key input needs known-hosts too"

ssh_act setup SSH_KEY="$key"$'\r\n' KNOWN_HOSTS=$'shop.example ssh-ed25519 AAAAnew\r\n'
k="$work/home/.ssh/roadie_action_key"
mode=$(stat -c %a "$k" 2>/dev/null || stat -f %Lp "$k")
if [ "$code" = 0 ] && [ "$mode" = 600 ] && [ "$(cat "$k")" = "$key" ] && [ "$(tail -c 1 "$k" | od -An -c | tr -d ' ')" = '\n' ]; then
	ok "the key is written 0600 with one final newline and no CR"
else bad "the key is written 0600 with one final newline and no CR"; fi
check "the key reaches the log masked" contains "$work/log" "::add-mask::b3BlbnNzaC1rZXktdjEAAAAAfakefakefake"
check "the key itself isn't in the log" bash -c '! grep -v "::add-mask::" "$1" | grep -q fakefake' _ "$work/log"
check "known_hosts keeps the old hosts and adds the new" \
	bash -c '[ "$(cat "$1")" = "$(printf "old.example ssh-ed25519 AAAAold\nshop.example ssh-ed25519 AAAAnew")" ]' _ "$work/home/.ssh/known_hosts"
check "config starts with the key under Host * and keeps the old blocks" \
	bash -c '[ "$(sed -n 1,2p "$1")" = "$(printf "Host *\n  IdentityFile ~/.ssh/roadie_action_key")" ] && grep -qx "Host old" "$1"' _ "$work/home/.ssh/config"
check "host key checking is never turned off" lacks "$work/home/.ssh/config" StrictHostKeyChecking

ssh_act cleanup
if [ "$code" = 0 ] && [ ! -e "$k" ] && cmp -s "$work/config.before" "$work/home/.ssh/config" &&
	cmp -s "$work/known_hosts.before" "$work/home/.ssh/known_hosts" && [ ! -e "$work/runner/temp/roadie-ssh" ]; then
	ok "cleanup removes the key and restores config and known_hosts"
else bad "cleanup removes the key and restores config and known_hosts"; fi

rm -rf "${work:?}/home" "${work:?}/runner" && mkdir -p "$work/home" "$work/runner/temp"
ssh_act setup SSH_KEY="$key" KNOWN_HOSTS="shop.example ssh-ed25519 AAAAnew"
ssh_act cleanup
if [ "$code" = 0 ] && [ -d "$work/home/.ssh" ] && [ -z "$(ls -A "$work/home/.ssh")" ]; then
	ok "cleanup removes files that weren't there before"
else bad "cleanup removes files that weren't there before"; fi
ssh_act cleanup
check "cleanup without a setup does nothing" [ "$code" = 0 ]
ssh_act setup
check "no ssh-key, nothing to set up" bash -c '[ "$1" = 0 ] && ! [ -e "$2/.ssh/roadie_action_key" ]' _ "$code" "$work/home"

# --- install ----------------------------------------------------------------------

act action-install.sh check ACTION_REF=v2.3.4
if [ "$code" = 0 ] && out version=2.3.4 && out skip=false && lacks "$work/log" "::warning::"; then
	ok "the Action's tag picks the version"
else bad "the Action's tag picks the version"; fi
act action-install.sh check ACTION_REF=v1
if [ "$code" = 0 ] && out version=9.8.7 && contains "$work/log" '::warning::Roadie'"'"'s version isn'"'"'t pinned: the action ref "v1"'; then
	ok "a major tag takes the latest release and warns"
else bad "a major tag takes the latest release and warns"; fi
act action-install.sh check VERSION=v1.0.0 ACTION_REF=v2.3.4
check "the version input wins" out version=1.0.0
act action-install.sh check VERSION="1.0.0; rm -rf /"
check "a malformed version is refused" [ "$code" = 2 ]

installed() { # installed VERSION ARGS... runs check with VERSION already installed
	local v=$1
	shift
	rm -rf "$work/runner"
	mkdir -p "$work/runner/temp/roadie-bin"
	printf '#!/bin/sh\necho "rhythm-roadie %s (commit abc1234)"\n' "$v" >"$work/runner/temp/roadie-bin/rhythm-roadie"
	chmod +x "$work/runner/temp/roadie-bin/rhythm-roadie"
	: >"$work/runner/output"
	: >"$work/runner/path"
	env GITHUB_OUTPUT="$work/runner/output" GITHUB_PATH="$work/runner/path" RUNNER_TEMP="$work/runner/temp" "$@" \
		"$here/action-install.sh" check >"$work/log" 2>&1
	code=$?
}
installed 1.2.3 VERSION=1.2.3
if [ "$code" = 0 ] && out skip=true && contains "$work/log" "Roadie 1.2.3 is already installed; skipping the download." &&
	has "$work/runner/path" "$work/runner/temp/roadie-bin"; then
	ok "a matching install is reused"
else bad "a matching install is reused"; fi
installed 0.0.0-test VERSION=0.0.0-test
check "a test build is reused by its version" out skip=true
installed 1.2.2 VERSION=1.2.3
check "another version is replaced" out skip=false

# --- verify-release.sh ---------------------------------------------------------------

rel="$work/release"
release() { # release: a fresh download folder with a matching checksum
	rm -rf "$rel" && mkdir -p "$rel"
	printf 'archive bytes' >"$rel/rhythm-roadie_linux_amd64.tar.gz"
	(cd "$rel" && { sha256sum rhythm-roadie_linux_amd64.tar.gz 2>/dev/null || shasum -a 256 rhythm-roadie_linux_amd64.tar.gz; } >checksums.txt)
	echo '{}' >"$rel/checksums.txt.sigstore.json"
}
verify_release() {
	"$here/verify-release.sh" "$rel" rhythm-roadie_linux_amd64.tar.gz "$@" >"$work/log" 2>&1
	code=$?
}
release
verify_release 1.0.0
if [ "$code" = 0 ] && has "$work/cosign-args" refs/tags/v1.0.0 && has "$work/cosign-args" RhythmShahriar/roadie &&
	has "$work/cosign-args" https://token.actions.githubusercontent.com; then
	ok "a signed release with a matching checksum verifies, for its own tag"
else bad "a signed release with a matching checksum verifies, for its own tag"; fi
FAKE_COSIGN_FAIL=1 verify_release 1.0.0
check "a bad signature is refused" bash -c '[ "$1" = 1 ] && grep -q "isn.t signed by Roadie.s release workflow" "$2"' _ "$code" "$work/log"
printf 'x' >>"$rel/rhythm-roadie_linux_amd64.tar.gz"
verify_release 1.0.0
check "a changed archive is refused" bash -c '[ "$1" = 1 ] && grep -q "doesn.t match its checksum" "$2"' _ "$code" "$work/log"
release
line=$(sed -n 1p "$rel/checksums.txt")
printf '%s\n' "$line" >>"$rel/checksums.txt"
verify_release 1.0.0
check "a second line for the archive is refused" bash -c '[ "$1" = 1 ] && grep -q "more than once" "$2"' _ "$code" "$work/log"
release
rm "$rel/checksums.txt.sigstore.json"
verify_release 1.0.0
check "a missing bundle is refused" [ "$code" = 1 ]
release
verify_release "1.0.0; id"
check "a malformed version is refused before cosign runs" [ "$code" = 1 ]

# --- the pull request comment ---------------------------------------------------------

printf '## Roadie: PASS (copy)\n\nshort\n' >"$work/summary.md"
"$here/action-comment.sh" body "$work/summary.md" >"$work/body" 2>"$work/log"
check "the comment starts with its marker" bash -c '[ "$(sed -n 1p "$1")" = "<!-- rhythm-roadie -->" ] && grep -q "short" "$1"' _ "$work/body"
{
	printf '## Roadie\n\n<details>\n\n'
	for i in $(seq 1 400); do printf '| row %s | some text to fill the comment |\n' "$i"; done
} >"$work/long.md"
ROADIE_COMMENT_LIMIT=2000 "$here/action-comment.sh" body "$work/long.md" >"$work/body" 2>"$work/log"
if [ "$(wc -c <"$work/body")" -lt 2000 ] && grep -q "The report is cut here" "$work/body" && grep -qx '</details>' "$work/body"; then
	ok "a long summary is cut at a line and its details closed"
else bad "a long summary is cut at a line and its details closed"; fi
"$here/action-comment.sh" body "$work/none.md" >"$work/log" 2>&1
check "no summary, no comment" contains "$work/log" "no summary to comment with"

exit "$fail"
