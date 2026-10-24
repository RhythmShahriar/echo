#!/usr/bin/env bash
# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Writes what the GitHub Action shows outside the run itself: the job
# summary. On a public repository anyone can read it, so by default it
# carries counts, change titles and action names, with recipients, phone
# numbers, subjects and quoted values masked. The Action shows the full
# `rhythm-echo report --md` instead when summary-details is true.
#
#   summary.sh report <report.json>   masked Markdown for a run or comparison
#   summary.sh mask                   mask stdin line by line (logs, errors)
#
# <report.json> is `rhythm-echo report --json`. Needs jq.
set -euo pipefail

# mask hides who a message went to and what it said: email addresses keep
# only their domain, international numbers and quoted text (subjects,
# events, values in titles) become ***, and so does the subject in a
# `rhythm-echo up --plain` line ("12:00:01 email to x: Subject [sealed]").
# $tok is Echo's token, hidden too in case a log line repeats it.
# shellcheck disable=SC2016 # jq source, not shell
jqlib='
def mask:
  (if $tok != "" then gsub($tok; "***") else . end)
  | gsub("[A-Za-z0-9._%+-]+@(?<d>([A-Za-z0-9-]+\\.)+[A-Za-z]{2,})"; "***@\(.d)")
  | gsub("\\+[0-9][0-9 ().-]{6,}[0-9]"; "+***")
  | gsub("\"[^\"]*\""; "\"***\"")
  | sub("^(?<h>[0-9]{2}:[0-9]{2}:[0-9]{2} .+? to [^:]*: ).*(?<s> \\[[a-z_ -]+\\])$"; "\(.h)***\(.s)");
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

tok=${RHYTHM_ECHO_TOKEN:-}

case ${1:-} in
mask)
	jq -Rr --arg tok "$tok" "$jqlib mask"
	;;
report)
	[ $# -eq 2 ] || { echo "usage: summary.sh report <report.json>" >&2; exit 2; }
	if jq -e 'has("changes")' "$2" >/dev/null; then
		jq -r --arg tok "$tok" "$jqlib $comparison" "$2"
	else
		jq -r --arg tok "$tok" "$jqlib $run" "$2"
	fi
	echo "$hidden"
	;;
*)
	echo "usage: summary.sh report <report.json> | mask" >&2
	exit 2
	;;
esac
