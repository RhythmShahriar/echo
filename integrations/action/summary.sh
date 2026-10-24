#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Writes what the GitHub Action shows outside the run itself: the job
# summary and the pull request comment. On a public repository anyone can
# read them, so by default they carry counts, change titles and action
# names, with recipients, phone numbers, subjects and quoted values masked.
# The Action shows the full `rhythm-echo report --md` instead when
# summary-details is true.
#
#   summary.sh report <report.json>               masked Markdown for a run or comparison
#   summary.sh comment [--details] <report.json>  pull request comment: what broke and what is new
#   summary.sh error <code> <stderr> <log>        why the command couldn't run, with the log's tail
#   summary.sh mask [--keep-quotes]               mask stdin line by line (logs, errors)
#
# <report.json> is `rhythm-echo report --json`. Needs jq.
set -euo pipefail

# mask hides who a message went to and what it said: email addresses keep
# only their domain, phone numbers and quoted text (subjects, events,
# values in titles) become ***. A number without a leading + counts as a
# phone number when it follows "to " or a comma and has 7 or more digits,
# so times and dates stay readable. Single quotes must stand apart from
# words ('x', not the one in "can't") and may hold an apostrophe; curly
# quotes count too. The subject in a `rhythm-echo up --plain` line
# ("12:00:01 email to x: Subject [sealed]") is masked as well.
# $tok is Echo's token, hidden too in case a log line repeats it. $raw
# (summary-details) keeps everything but the token; $keepq keeps quoted
# text, for error messages that quote what was wrong.
# shellcheck disable=SC2016 # jq source, not shell
jqlib='
def mask:
  gsub("\u001b\\[[0-9;]*[A-Za-z]"; "")
  | (if $tok != "" then gsub($tok; "***") else . end)
  | if $raw then . else
      gsub("[A-Za-z0-9._%+-]+@(?<d>([A-Za-z0-9-]+\\.)+[A-Za-z]{2,})"; "***@\(.d)")
      | gsub("\\+[0-9][0-9 ().-]{6,}[0-9]"; "+***")
      | gsub("(?<p>(?:\\bto|,) )(?<n>\\(?[0-9][0-9 ().-]{5,}[0-9])";
          "\(.p)\(if (.n | gsub("[^0-9]"; "") | length) >= 7 and (.n | test("^[0-9]{4}-[0-9]{2}-[0-9]{2}$") | not) then "***" else .n end)")
      | (if $keepq then . else
          gsub("\"[^\"]*\""; "\"***\"")
          | gsub("(?<![A-Za-z0-9])\u0027(?:[^\u0027]|\u0027(?=[A-Za-z]))*\u0027(?![A-Za-z0-9])"; "\u0027***\u0027")
          | gsub("\u2018(?:[^\u2019]|\u2019(?=[A-Za-z]))*\u2019"; "\u2018***\u2019")
          | gsub("\u201c[^\u201d]*\u201d"; "\u201c***\u201d")
        end)
      | sub("^(?<h>[0-9]{2}:[0-9]{2}:[0-9]{2} .+? to [^:]*: ).*(?<s> \\[[a-z_ -]+\\])$"; "\(.h)***\(.s)")
    end;
# unwrap takes the run or comparison out of the JSON envelope Echo writes
# (schema, kind, summary, data). A comparison envelope holds the compared
# actions as a list with their count in summary, so the count goes back
# in. Plain JSON from an older Echo passes unchanged.
def unwrap: if type == "object" and (.kind | type) == "string" and has("data") then
    (if .kind == "comparison" then .data + {actions: .summary.actions} else .data end)
  else . end;
def cell: gsub("\\|"; "\\|") | gsub("[\r\n]+"; " ") | gsub("<"; "&lt;") | gsub(">"; "&gt;");
def plural($n; $one; $many): if $n == 1 then "1 \($one)" else "\($n) \($many)" end;
def actions: (.actions // []) as $a
  | if ($a | length) == 0 then "" elif ($a | length) <= 3 then $a | join(", ")
    else "\($a[:3] | join(", ")) +\(($a | length) - 3) more" end;
'

# shellcheck disable=SC2016 # Markdown backticks, not shell
hidden='<sub>Recipients, subjects and message contents are hidden here. Set `summary-details: true` to show them, or open the HTML report.</sub>'

# shellcheck disable=SC2016 # jq source, not shell
comparison='
def count($c): [.changes[] | select(.class == $c)] | length;
def glyph: {"broke": "✗", "new": "▲", "changed": "■", "fixed": "✓"}[.];
. as $r
| (["broke", "new", "changed", "fixed"] | map(. as $c | {key: $c, value: ($r | count($c))}) | from_entries) as $n
| "## ♪ Echo\n",
  "**\(if $n.broke > 0 then "Something broke"
       elif $n.new > 0 then "New messages or problems"
       elif $n.changed > 0 then "Nothing lost; some messages changed"
       else "Everything is still sent the same way" end).** \(plural(.actions; "action"; "actions")) compared with \(if .before.name then "snapshot \"\(.before.name)\"" else "the earlier run" end).\n",
  "| Broke | New | Changed | Fixed |\n|---:|---:|---:|---:|\n| \($n.broke) | \($n.new) | \($n.changed) | \($n.fixed) |\n",
  (["broke", "new", "changed", "fixed"][] as $c
   | [$r.changes[] | select(.class == $c)] | select(length > 0)
   | "### \($c | glyph) \($c | ascii_upcase) (\(length))\n\n| Change | Actions |\n|---|---|",
     (.[] | "| \(.title | mask | cell) | \(actions | cell) |"), ""),
  (if (.warnings // []) | length > 0 then plural(.warnings | length; "note"; "notes") + " in the full report.\n" else empty end)
'

# shellcheck disable=SC2016 # jq source, not shell
run='
def noun: {"email": ["email", "emails"], "sms": ["SMS", "SMS"], "whatsapp": ["WhatsApp message", "WhatsApp messages"],
  "chat": ["chat message", "chat messages"], "webhook": ["webhook", "webhooks"], "analytics": ["analytics event", "analytics events"],
  "payment": ["payment call", "payment calls"], "push": ["push notification", "push notifications"], "http": ["request", "requests"]}[.] // [., .];
def order: ["email", "sms", "whatsapp", "chat", "webhook", "analytics", "payment", "push", "http"];
def tally: [.[].channel] as $ch | if ($ch | length) == 0 then "no messages"
  else (order + ($ch - order | unique)) | map(. as $c | ($c | noun) as $w | [$ch[] | select(. == $c)] | length
    | select(. > 0) | plural(.; $w[0]; $w[1])) | join(", ") end;
def uniq: reduce .[] as $x ([]; if any(.[]; . == $x) then . else . + [$x] end);
def rank: {"error": 0, "warning": 1, "notice": 2}[.] // 3;
. as $r
| ([(.actions // [])[] as $a | ($a.issues // [])[] | . + {action: $a.label}]
   + [(.issues // [])[] | . + {action: "site-wide"}]
   | to_entries | map(.value + {seen: .key})
   | group_by(.key) | map(.[0] + {actions: (map(.action) | uniq)})
   | sort_by((.level | rank), -(.actions | length), .seen)) as $issues
| ([(.actions // [])[] | select(.error != null and .error != "")] | length) as $failed
| ([$issues[] | select(.level == "error")] | length) as $errors
| ([$issues[] | select(.level == "warning")] | length) as $warnings
| "## ♪ Echo\n",
  "**\(if .name then "Snapshot \"\(.name)\" saved; " else "" end)\(if $failed > 0 then plural($failed; "action failed"; "actions failed")
       elif $errors > 0 then "messages have errors" elif $warnings > 0 then "a few warnings" else "all clear" end).** \(plural((.actions // []) | length; "action"; "actions")) run; \([(.actions // [])[].messages[]?] | tally) sent.\n",
  "| Action | Sent | Issues |\n|---|---|---:|",
  ((.actions // [])[] | "| \(.label | mask | cell) | \(if (.error // "") != "" then "failed: " + (.error | split("\n")[0] | mask | cell) else ((.messages // []) | tally) end) | \((.issues // []) | length) |"),
  "",
  (if ($issues | length) > 0 then
     "| Level | Issue | Actions |\n|---|---|---|",
     ($issues[] | "| \(.level) | \(.title | mask | cell) | \(actions | cell) |"), ""
   else empty end),
  (if (.warnings // []) | length > 0 then plural(.warnings | length; "note"; "notes") + " in the full report.\n" else empty end)
'

# shellcheck disable=SC2016 # jq source, not shell
comment='
def item: "- \(.title | mask | cell)\(if (.actions // []) | length > 0 then " (\(actions | cell))" else "" end)";
def list($head; $items): if ($items | length) > 0 then "**\($head) (\($items | length))**\n", ($items[] | item), "" else empty end;
"## ♪ Echo\n",
(if has("changes") then
   [.changes[] | select(.class == "broke")] as $broke
   | [.changes[] | select(.class == "new")] as $new
   | [.changes[] | select(.class == "changed")] as $changed
   | "**\(if ($broke | length) > 0 then "Something broke" elif ($new | length) > 0 then "New messages or problems"
         elif ($changed | length) > 0 then "Nothing lost; some messages changed" else "Everything is still sent the same way" end).** Compared with \(if .before.name then "snapshot \"\(.before.name | mask)\"" else "the earlier run" end): \($broke | length) broke, \($new | length) new, \($changed | length) changed.\n",
     list("✗ BROKE"; $broke), list("▲ NEW"; $new)
 else
   [(.actions // [])[] | select((.error // "") != "") | {title: ("Action fails: " + (.error | split("\n")[0])), actions: [.label]}] as $failed
   | ([(.actions // [])[] as $a | ($a.issues // [])[] | select(.level == "error") | . + {actions: [$a.label]}]
      + [(.issues // [])[] | select(.level == "error") | . + {actions: ["site-wide"]}]) as $errors
   | "**\(if ($failed | length) + ($errors | length) > 0 then "Something is wrong" else "No errors" end).** \(plural((.actions // []) | length; "action"; "actions")) run: \($failed | length) failed, \($errors | length) with errors.\n",
     list("✗ FAILED"; $failed), list("✗ ERRORS"; $errors)
 end),
"<sub>Everything else is in the job summary\(if $run != "" then " of [this run](\($run))" else "" end).</sub>"
'

tok=${RHYTHM_ECHO_TOKEN:-}
run_url=""
[ -z "${GITHUB_RUN_ID:-}" ] || run_url="${GITHUB_SERVER_URL:-https://github.com}/${GITHUB_REPOSITORY:-}/actions/runs/$GITHUB_RUN_ID"

# q runs a jq program with the shared definitions and masking switches.
q() {
	local raw=$1 keepq=$2
	shift 2
	jq --arg tok "$tok" --arg run "$run_url" --argjson raw "$raw" --argjson keepq "$keepq" "$@"
}

# fence prints stdin as a Markdown code block that its contents can't close.
fence() {
	echo '```'
	# shellcheck disable=SC2016 # backticks, not an expansion
	sed 's/```/` ` `/g'
	echo '```'
}

case ${1:-} in
mask)
	keepq=false
	[ "${2:-}" = --keep-quotes ] && keepq=true
	q false "$keepq" -Rr "$jqlib mask"
	;;
report)
	[ $# -eq 2 ] || { echo "usage: summary.sh report <report.json>" >&2; exit 2; }
	if q false false -e "$jqlib unwrap | has(\"changes\")" "$2" >/dev/null; then
		q false false -r "$jqlib unwrap | $comparison" "$2"
	else
		q false false -r "$jqlib unwrap | $run" "$2"
	fi
	echo "$hidden"
	;;
comment)
	raw=false
	if [ "${2:-}" = --details ]; then
		raw=true
		shift
	fi
	[ $# -eq 2 ] || { echo "usage: summary.sh comment [--details] <report.json>" >&2; exit 2; }
	q "$raw" false -r "$jqlib unwrap | $comment" "$2"
	;;
error)
	[ $# -eq 4 ] || { echo "usage: summary.sh error <code> <stderr> <log>" >&2; exit 2; }
	code=$2 errf=$3 log=$4
	case $code in
	2) title="Echo didn't run: the command line or the config has a mistake (exit 2)." ;;
	3) title="Echo couldn't run (exit 3)." ;;
	130) title="Echo was interrupted (exit 130)." ;;
	*) title="Echo stopped with exit code $code." ;;
	esac
	printf '## ♪ Echo\n\n**%s**\n\n' "$title"
	# The CLI's own error lines, or the end of what it printed.
	err=""
	if [ -s "$errf" ]; then
		err=$(grep -E '^(rhythm echo:|::error::|Error:)' "$errf" || true)
		[ -n "$err" ] || err=$(tail -n 10 "$errf")
	fi
	if [ -n "$err" ]; then
		printf '%s\n' "$err" | q false true -Rr "$jqlib mask" | fence
		echo
	fi
	if [ -s "$log" ]; then
		printf '<details><summary>The last 30 lines of echo.log</summary>\n\n'
		tail -n 30 "$log" | q false false -Rr "$jqlib mask" | fence
		printf '\n</details>\n\n'
	fi
	echo '<sub>Recipients and subjects are hidden here. The echo-log artifact has the whole log (log: true, the default).</sub>'
	;;
*)
	echo "usage: summary.sh report|comment|error|mask (see the top of this file)" >&2
	exit 2
	;;
esac
