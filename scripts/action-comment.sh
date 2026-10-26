#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Posts Roadie's job summary as one pull request comment for the GitHub
# Action (pr-comment: true). Later runs update that comment instead of
# adding another.
#
#   action-comment.sh body MARKDOWN   prints the comment body
#   action-comment.sh post MARKDOWN   creates or updates the comment
#
# MARKDOWN is the summary action-run.sh wrote: counts only, or the full
# report with summary-details. The comment is found again by its first
# line, the marker below and by its author, the token's own login: anyone
# can post a comment that starts with the marker. Updating theirs
# would show Roadie's result under their name. A body over GitHub's
# 65,536 character limit is cut at a line.
#
# post uses gh with GH_TOKEN and needs GITHUB_REPOSITORY and
# GITHUB_EVENT_PATH. Posting needs "permissions: pull-requests: write";
# when it fails the step warns and goes on.
set -euo pipefail

marker='<!-- rhythm-roadie -->'
limit=${ROADIE_COMMENT_LIMIT:-60000}

body() {
	local md=$1 footer="" kept
	if [ -n "${GITHUB_SERVER_URL:-}" ] && [ -n "${GITHUB_REPOSITORY:-}" ] && [ -n "${GITHUB_RUN_ID:-}" ]; then
		footer="[Workflow run]($GITHUB_SERVER_URL/$GITHUB_REPOSITORY/actions/runs/$GITHUB_RUN_ID)"
	fi
	local room=$((limit - ${#marker} - ${#footer} - 200))
	printf '%s\n' "$marker"
	if [ "$(wc -c <"$md")" -le "$room" ]; then
		cat "$md"
	else
		# Whole lines only, so a table row is never cut in half.
		kept=$(LC_ALL=C awk -v room="$room" '{ n += length($0) + 1; if (n > room) exit; print }' "$md")
		printf '%s\n' "$kept"
		# A cut inside the details leaves <details> open.
		if [ "$(grep -c '<details>' <<<"$kept")" -gt "$(grep -c '</details>' <<<"$kept")" ]; then
			printf '\n</details>\n'
		fi
		printf '\n_The report is cut here to fit a comment; the job summary has all of it._\n'
	fi
	[ -z "$footer" ] || printf '\n%s\n' "$footer"
}

# pr prints the pull request number of this run, if there is one.
pr() {
	jq -r '.pull_request.number // (if .issue.pull_request then .issue.number else empty end) // empty' "${GITHUB_EVENT_PATH:?}"
}

# me prints the login the token comments as. GITHUB_TOKEN can't read
# /user and comments as github-actions[bot]; ROADIE_COMMENT_AUTHOR names
# another app's bot.
me() {
	if [ -n "${ROADIE_COMMENT_AUTHOR:-}" ]; then
		printf '%s' "$ROADIE_COMMENT_AUTHOR"
		return
	fi
	local login
	if login=$(gh api user --jq .login 2>/dev/null) && [[ $login =~ ^[A-Za-z0-9][A-Za-z0-9-]*$ ]]; then
		printf '%s' "$login"
	else
		printf 'github-actions[bot]'
	fi
}

warn() {
	echo "::warning::Roadie: $1 on the pull request. The job needs \"permissions: pull-requests: write\"; pull requests from forks only get a read-only token."
}

post() {
	local md=$1 n id payload login
	: "${GITHUB_REPOSITORY:?}"
	n=$(pr)
	if [ -z "$n" ]; then
		echo "::notice::Roadie: no pull request for this run (event ${GITHUB_EVENT_NAME:-unknown}), so no comment was posted."
		return 0
	fi
	payload=$(mktemp)
	trap 'rm -f "$payload"' RETURN
	body "$md" | jq -Rs '{body: .}' >"$payload"
	login=$(me)
	if ! id=$(gh api --paginate "repos/$GITHUB_REPOSITORY/issues/$n/comments" \
		--jq ".[] | select(.user.login == $(jq -n --arg l "$login" '$l') and (.body | startswith(\"$marker\"))) | .id" | sed -n 1p); then
		warn "couldn't read the comments"
		return 0
	fi
	if [ -n "$id" ]; then
		gh api -X PATCH "repos/$GITHUB_REPOSITORY/issues/comments/$id" --input "$payload" >/dev/null ||
			{
				warn "couldn't update comment $id"
				return 0
			}
		echo "Updated the Roadie comment on pull request #$n."
	else
		gh api -X POST "repos/$GITHUB_REPOSITORY/issues/$n/comments" --input "$payload" >/dev/null ||
			{
				warn "couldn't comment"
				return 0
			}
		echo "Commented on pull request #$n."
	fi
}

cmd=${1:-}
md=${2:-}
[ -n "$md" ] || {
	echo "usage: action-comment.sh body|post MARKDOWN" >&2
	exit 2
}
[ -f "$md" ] || {
	echo "::warning::Roadie: no summary to comment with ($md)"
	exit 0
}
case "$cmd" in
body) body "$md" ;;
post) post "$md" ;;
*)
	echo "usage: action-comment.sh body|post MARKDOWN" >&2
	exit 2
	;;
esac
