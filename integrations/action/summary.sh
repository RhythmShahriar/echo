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
#   summary.sh consent <consent.json> [<report.md>]  mode consent: Echo's Markdown or a table
#   summary.sh consent-outputs <consent.json>     mode consent: the Action's outputs as name=value
#
# <report.json> is `rhythm-echo report --json`; <consent.json> is the
# envelope of `rhythm-echo consent -o x.json`. Needs jq.
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

# consent is the summary of a consent check when Echo's own Markdown is
# missing: tracker names, counts, check titles, page paths and why a run
# couldn't choose. Never query strings, cookie values, IDs, details or
# screenshots. Everything a page could have put there is escaped (md), so
# it can't mention anyone, link anywhere or add HTML.
# shellcheck disable=SC2016 # jq source, not shell
consent='
def md: tostring | mask | gsub("[\r\n\t]+"; " ")
  | gsub("(?<c>[&<>|`\\[\\]@!*_#\\\\~:.])"; "&#\(.c | explode[0]);");
# path keeps the path of a URL: no scheme, host, query or fragment.
def path: tostring | sub("^[A-Za-z][A-Za-z0-9+.-]*://[^/?#]*"; "") | sub("[?#].*$"; "") | if . == "" then "/" else . end;
def runname: {"none": "No choice", "accept": "Accept", "reject": "Reject", "gpc": "GPC", "withdraw": "Withdraw"}[.] // .;
def uniq: reduce .[] as $x ([]; if any(.[]; . == $x) then . else . + [$x] end);
(.summary // {}) as $s
| (.data // {}) as $d
| ([($d.issues // [])[] | select(.ignored == null)]) as $issues
| ([$d.comparison.changes[]? | select(.accepted != true)]) as $changes
| ($s.inconclusive_runs // 0) as $inc
| (if ($s.errors // 0) + ($s.warnings // 0) + ($s.broke // 0) + ($s.new // 0) + ($s.changed // 0) > 0 then "Findings"
   elif $inc > 0 then "Couldn\u0027t check" else "All clear" end) as $verdict
| (["none", "accept", "reject", "gpc", "withdraw"] as $order
   | [($d.pages // [])[].runs[]?.run] | uniq | (($order - ($order - .)) + (. - $order))) as $kinds
| "## ♪ Echo consent\n",
  "**\($verdict).** \(plural($s.pages // 0; "page"; "pages")), \(plural($s.runs // 0; "run"; "runs")), \(plural($s.trackers // 0; "tracker"; "trackers")), policy \(($d.policy // "advanced") | md). Hits before the choice: \($s.before_choice // 0). After Reject: \($s.after_reject // 0).\(if $inc > 0 then " Couldn\u0027t check \(plural($inc; "run"; "runs"))." else "" end)\n",
  (if ($d.pages // []) | length > 0 then
     "| Page | Banner | \($kinds | map(runname) | join(" | ")) |\n|---|---|\($kinds | map("---|") | join(""))",
     (($d.pages // [])[] as $p
      | ($p.url | path) as $path
      | "| \($path | md) | \(if $p.cmp.found then ($p.cmp.name | md) elif $p.cmp.present then "\($p.cmp.name | md) (not shown)" else "none" end) | \(
          [$kinds[] as $k | ([$p.runs[]? | select(.run == $k)] | first) as $r
           | if $r == null then "" elif ($r.reason // "") != "" then "? \($r.reason | md)"
             else ([$r.hits[]?.name] | uniq) as $names
             | ([$issues[] | select((.level == "error" or .level == "warning") and .run == $k and (.page == $path or .page == $p.url))] | length > 0) as $bad
             | "\(if $bad then "✗ " else "" end)\(if ($names | length) == 0 then "nothing fired" else ($names | map(md) | join(", ")) end)"
             end] | join(" | ")) |"),
     ""
   else empty end),
  (["error", "warning", "notice"][] as $l
   | [$issues[] | select(.level == $l)] | select(length > 0)
   | "### \({"error": "Errors", "warning": "Warnings", "notice": "Notices"}[$l]) (\(length))\n\n| Check | Page | Run | What |\n|---|---|---|---|",
     (.[] | "| \(.check | md) | \(.page | path | md) | \(.run | runname | md) | \(.title | md) |"), ""),
  (if ($changes | length) > 0 then
     "### Against the baseline (\($changes | length))\n\n| Change | Page | Run | What |\n|---|---|---|---|",
     ($changes[] | "| \(.class | ascii_upcase | md) | \(.page | path | md) | \(.run | runname | md) | \(.title | md) |"), ""
   else empty end),
  (if ($d.inconclusive // []) | length > 0 then
     "### Couldn\u0027t check (\($d.inconclusive | length))\n\n| Page | Run | Why |\n|---|---|---|",
     ($d.inconclusive[] | "| \(.page | path | md) | \(.run | runname | md) | \(.reason | md) |"), ""
   else empty end),
  "_Evidence, not legal advice. Hits held in the browser._\n"
'

# shellcheck disable=SC2016 # Markdown backticks, not shell
consent_hidden='<sub>Query strings, cookie values, IDs and screenshots are hidden here. The evidence folder (upload: true) has report.html and consent.json.</sub>'

# cut keeps stdin under the job summary's limit (1 MiB a step): at most
# 900 KiB, cut at a line, with a note where it was cut.
cut_at=$((900 * 1024))
cut() {
	local f
	f=$(mktemp)
	cat > "$f"
	if [ "$(wc -c < "$f")" -le "$cut_at" ]; then
		cat "$f"
	else
		head -c $((cut_at - 200)) "$f" | sed '$d'
		printf '\n_The summary was cut at 900 KiB: the evidence folder (upload: true) has the whole report._\n'
	fi
	rm -f "$f"
}

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
consent)
	[ $# -eq 2 ] || [ $# -eq 3 ] || { echo "usage: summary.sh consent <consent.json> [<report.md>]" >&2; exit 2; }
	{
		if [ $# -eq 3 ] && [ -s "$3" ]; then
			# Echo's own Markdown, already escaped and masked by Echo:
			# addresses and phone numbers are masked once more here, quoted
			# text is kept (a fix quotes code).
			q false true -Rr "$jqlib mask" "$3"
		else
			q false false -r "$jqlib $consent" "$2"
			echo "$consent_hidden"
		fi
	} | cut
	;;
consent-outputs)
	[ $# -eq 2 ] || { echo "usage: summary.sh consent-outputs <consent.json>" >&2; exit 2; }
	# One name=value a line for GITHUB_OUTPUT; a newline in the folder
	# would add an output, so such a folder is left out.
	q false false -r '"before-choice=\(.summary.before_choice // 0)", "after-reject=\(.summary.after_reject // 0)",
		"inconclusive=\(.summary.inconclusive_runs // 0)",
		(.data.evidence.folder // "" | select(. != "" and (test("[\r\n]") | not)) | "folder=\(.)")' "$2"
	;;
*)
	echo "usage: summary.sh report|comment|error|mask|consent|consent-outputs (see the top of this file)" >&2
	exit 2
	;;
esac
