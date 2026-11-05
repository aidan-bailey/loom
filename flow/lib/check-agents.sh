#!/usr/bin/env bash
# lib/check-agents.sh — lint the flow plugin's agents and skills (plugin/agents,
# plugin/skills).
#
# The harness fails OPEN on agent definitions: a `tools:` entry naming a tool
# it does not provide is dropped with no error (this is how both reviewers
# once shipped with phantom Grep/Glob entries and without SendMessage), and
# an unpinned `effort:` inherits the dispatching SESSION's effort rather
# than a cheap default. Nothing at dispatch time reports either. A third
# hides at startup: a `skills:` entry naming a skill that is missing, or
# that sets disable-model-invocation: true, is skipped with only a
# debug-log warning. A fourth is a quiet default rather than a failure: a
# subagent's prompt cache lives five minutes unless `experimental.cacheTtl`
# says otherwise. This lint makes all four loud at verification time
# instead, reading each agent's body together with the skills it preloads
# from skills/. It also caps every agent's and skill's description at 400
# characters, since every session in every repo loads them; fails a skill no
# agent preloads unless it sets disable-model-invocation: true, since such a
# skill is an owner-started command the model must not start unasked; and fails
# any agent, skill or doc holding a word from lib/banned-tokens.txt (or from
# $FLOW_DATA/banned-tokens.txt, the owner's private list, when it exists):
# the flow serves every repo, so none may name one repo's tools. Runs
# standalone or via lib/test.sh.
#
# Extending VALID_TOOLS: verify the name exists first in a live session with
# `ToolSearch select:<Name>` (it returns a schema only for real tools), then
# add it here. Never copy tools: lines from plugin agents written for a
# different harness.
#
# Exit codes:
#   0  all checks passed
#   1  one or more checks failed
#   2  invocation error

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

AGENTS_DIR="${AGENTS_DIR:-plugin/agents}"
SKILLS_DIR="${SKILLS_DIR:-plugin/skills}"
DOCS_DIR="plugin/docs"
BANNED="lib/banned-tokens.txt"
# Words that must not ship in loom's repo, such as client repo names.
PRIVATE_BANNED="${FLOW_DATA:-$HOME/.loom/flow}/banned-tokens.txt"
# The agents loom's git guard checks, and their modes.
GUARDED="lib/guarded-agents.txt"
DESCRIPTION_CAP=400

# ToolSearch-verified against this harness (last checked 2026-08-05). Agent
# joined on 2026-10-13 for plan-execution-supervisor: a subagent that lists it
# can spawn subagents, up to three layers below the main session.
VALID_TOOLS=(Agent Bash Read Write Edit SendMessage ToolSearch)
# The only definitions that may list Agent. A subagent definition ignores an
# Agent(type, …) list, so any agent holding Agent could spawn developers, or a
# general-purpose agent at the session's model, from inside a plan run.
SPAWNING_AGENTS=(plan-execution-supervisor)
VALID_MODELS=(haiku sonnet opus fable)
VALID_EFFORTS=(low medium high xhigh max)

FAIL=0
fail() { echo "FAIL [$1] $2"; FAIL=1; }

# Whether the frontmatter text in $1 sets disable-model-invocation: true (quotes and a trailing comment allowed).
disables_invocation() {
  grep -qE "^disable-model-invocation:[[:space:]]*[\"']?true[\"']?([[:space:]]+#.*)?[[:space:]]*\$" <<<"$1"
}

in_list() {
  local needle="$1" x
  shift
  for x in "$@"; do [[ "$x" == "$needle" ]] && return 0; done
  return 1
}

if [[ ! -d "$AGENTS_DIR" ]]; then
  echo "FAIL [dir] $AGENTS_DIR does not exist" >&2
  exit 2
fi

if [[ ! -d "$SKILLS_DIR" ]]; then
  echo "FAIL [dir] $SKILLS_DIR does not exist" >&2
  exit 2
fi


shopt -s nullglob
AGENT_FILES=("$AGENTS_DIR"/*.md)
# Every skill an agent preloads; any other skill is an owner-started command.
PRELOADED=()
if (( ${#AGENT_FILES[@]} == 0 )); then
  # Fail closed: an emptied roster must be a deliberate, visible act.
  fail empty "$AGENTS_DIR contains no agent definitions"
fi

for file in ${AGENT_FILES[@]+"${AGENT_FILES[@]}"}; do
  base="$(basename "$file" .md)"

  # Frontmatter = lines strictly between the first two '---' lines.
  fm="$(awk '/^---$/{n++; next} n==1{print} n>=2{exit}' "$file")"
  if [[ -z "$fm" ]]; then
    fail frontmatter "$file: no frontmatter block"
    continue
  fi

  field() { sed -n "s/^$1:[[:space:]]*//p" <<<"$fm" | head -1; }

  name="$(field name)"
  description="$(field description)"
  model="$(field model)"
  effort="$(field effort)"
  tools="$(field tools)"

  [[ -n "$name" ]] || fail name "$file: missing name:"
  if [[ -n "$name" && "$name" != "$base" ]]; then
    fail name "$file: name '$name' does not match filename '$base'"
  fi
  [[ -n "$description" ]] || fail description "$file: missing description:"
  (( ${#description} <= DESCRIPTION_CAP )) \
    || fail description "$file: description is ${#description} characters; the cap is $DESCRIPTION_CAP, because every session in every repo loads it"

  # An agent starts with its body plus the full text of every skill its
  # `skills:` field preloads, so the rule check below reads both. The harness
  # skips a preloaded skill that is missing, or that sets
  # disable-model-invocation: true, with only a debug-log warning — for the
  # implementers, whose shared habits live in a preloaded skill, that would
  # strip every rule without a sound.
  body="$(awk '/^---$/{n++; next} n>=2{print}' "$file")"
  text="$body"
  skills_inline="$(field skills)"
  if [[ -n "$skills_inline" ]]; then
    skills_inline="${skills_inline//[\[\]]/}"
    IFS=',' read -ra skills <<<"$skills_inline"
  else
    # YAML list form: "skills:" followed by "  - <name>" lines.
    mapfile -t skills < <(awk '/^skills:[[:space:]]*$/{f=1; next} f && /^[[:space:]]+-/{sub(/^[[:space:]]+-[[:space:]]*/, ""); print; next} f{exit}' <<<"$fm")
  fi
  for skill in ${skills[@]+"${skills[@]}"}; do
    skill="${skill#"${skill%%[![:space:]]*}"}"
    skill="${skill%"${skill##*[![:space:]]}"}"
    [[ -n "$skill" ]] || continue
    if [[ "$skill" != flow:* ]]; then
      fail skills "$file: preloads '$skill'; a plugin agent preloads a plugin skill by its namespaced name, flow:<name>"
      continue
    fi
    skill="${skill#flow:}"
    PRELOADED+=("$skill")
    skill_file="$SKILLS_DIR/$skill/SKILL.md"
    if [[ ! -f "$skill_file" ]]; then
      fail skills "$file: preloads '$skill', but $skill_file does not exist — the harness would skip it with only a debug-log warning"
      continue
    fi
    skill_fm="$(awk '/^---$/{n++; next} n==1{print} n>=2{exit}' "$skill_file")"
    if disables_invocation "$skill_fm"; then
      fail skills "$file: preloads '$skill', but $skill_file sets disable-model-invocation: true, which blocks preloading"
    fi
    text+=$'\n'"$(cat "$skill_file")"
  done

  # An agent that can edit files commits into the controller's shared
  # worktree, so loom's git guard must check its calls: Claude ignores hooks in
  # a plugin agent, so the guard is loom's PreToolUse hook (`loom flow guard`),
  # which checks the agents $GUARDED lists. A developer must also carry the
  # shared-index rules (read the index with `git diff-index` before a pathspec
  # commit, never rewrite history: `--amend` stands for the ban); the drafter
  # writes plan text under its drafts directory and never commits. Every
  # editing agent has a turn cap, so a loop stuck on a failing build stops.
  if grep -qE '^hooks:' <<<"$fm"; then
    fail guard "$file: sets hooks:, which Claude ignores in a plugin agent; loom's git guard covers the agents $GUARDED lists"
  fi
  if [[ "$tools" =~ (^|,)[[:space:]]*(Write|Edit)[[:space:]]*(,|$) ]]; then
    mode="$(awk -v n="$base" '!/^#/ && $1 == n { print $2 }' "$GUARDED" 2>/dev/null || true)"
    if [[ -z "$mode" ]]; then
      fail guard "$file: edits files but $GUARDED doesn't list it, so loom's git guard would not check its calls"
    elif [[ "$mode" == developer ]]; then
      for marker in 'diff-index' '--amend'; do
        grep -q -e "$marker" <<<"$text" \
          || fail git "$file: edits files but neither its body nor a preloaded skill mentions '$marker' — an implementer must read the shared index before a pathspec commit and must never rewrite history"
      done
    elif [[ "$mode" != drafter ]]; then
      fail guard "$GUARDED: '$base' has mode '$mode'; the modes are developer and drafter"
    fi
    [[ "$(field maxTurns)" =~ ^[1-9][0-9]*$ ]] \
      || fail guard "$file: edits files but sets no maxTurns — a runaway loop has no cap"
  fi

  if [[ -z "$model" ]]; then
    fail model "$file: missing model: (an unpinned model inherits the session's)"
  elif ! in_list "$model" "${VALID_MODELS[@]}"; then
    fail model "$file: model '$model' not in {${VALID_MODELS[*]}}"
  fi

  if [[ -z "$effort" ]]; then
    fail effort "$file: missing effort: (an unpinned effort inherits the SESSION's effort, not a cheap default)"
  elif ! in_list "$effort" "${VALID_EFFORTS[@]}"; then
    fail effort "$file: effort '$effort' not in {${VALID_EFFORTS[*]}}"
  fi

  # A subagent's prompt cache lives 5 minutes unless its definition says
  # otherwise, even on a subscription, so every definition chooses its
  # lifetime explicitly. Block form ("experimental:" then "  cacheTtl: 1h")
  # or flow form ("experimental: {cacheTtl: 1h}").
  cache_ttl="$(field experimental | sed -n 's/.*cacheTtl:[[:space:]]*\([0-9a-z]*\).*/\1/p')"
  if [[ -z "$cache_ttl" ]]; then
    cache_ttl="$(awk '/^experimental:[[:space:]]*$/{f=1; next} f && /^[[:space:]]+cacheTtl:/{sub(/^[[:space:]]+cacheTtl:[[:space:]]*/, ""); print; exit} f && /^[^[:space:]]/{exit}' <<<"$fm")"
  fi
  if [[ -z "$cache_ttl" ]]; then
    fail cache "$file: no experimental.cacheTtl — a subagent's prompt cache defaults to 5m even on a subscription; choose 5m or 1h"
  elif [[ "$cache_ttl" != "5m" && "$cache_ttl" != "1h" ]]; then
    fail cache "$file: cacheTtl '$cache_ttl' is ignored — Claude Code accepts only 5m or 1h"
  fi

  if [[ -z "$tools" ]]; then
    fail tools "$file: missing tools: (an absent allowlist grants the default tool set)"
    continue
  fi
  has_sendmessage=0
  IFS=',' read -ra TOOL_LIST <<<"$tools"
  for t in "${TOOL_LIST[@]}"; do
    # trim surrounding whitespace
    t="${t#"${t%%[![:space:]]*}"}"
    t="${t%"${t##*[![:space:]]}"}"
    if [[ -z "$t" ]]; then
      fail tools "$file: empty entry in tools: list"
      continue
    fi
    if ! in_list "$t" "${VALID_TOOLS[@]}"; then
      fail tools "$file: tool '$t' is not in the verified allowlist {${VALID_TOOLS[*]}} — phantom names are dropped SILENTLY at dispatch; verify with ToolSearch select:$t, then add it to VALID_TOOLS"
    fi
    if [[ "$t" == "Agent" ]] && ! in_list "$base" "${SPAWNING_AGENTS[@]}"; then
      fail tools "$file: lists Agent, but only {${SPAWNING_AGENTS[*]}} may spawn agents — Agent(type) lists are ignored in subagent definitions, so this agent could spawn any type"
    fi
    if [[ "$t" == "SendMessage" ]]; then
      has_sendmessage=1
    fi
  done
  (( has_sendmessage )) \
    || fail tools "$file: tools: lacks SendMessage — it cannot message the agent that dispatched it"
done

for skill_file in "$SKILLS_DIR"/*/SKILL.md; do
  skill_description="$(awk '/^---$/{n++; next} n==1 && /^description:/{sub(/^description:[[:space:]]*/, ""); print; exit}' "$skill_file")"
  [[ -n "$skill_description" ]] || fail description "$skill_file: missing description:"
  (( ${#skill_description} <= DESCRIPTION_CAP )) \
    || fail description "$skill_file: description is ${#skill_description} characters; the cap is $DESCRIPTION_CAP, because every session in every repo loads it"
  skill_name="$(basename "$(dirname "$skill_file")")"
  if ! in_list "$skill_name" ${PRELOADED[@]+"${PRELOADED[@]}"}; then
    skill_fm="$(awk '/^---$/{n++; next} n==1{print} n>=2{exit}' "$skill_file")"
    disables_invocation "$skill_fm" \
      || fail invocation "$skill_file: no agent preloads it, so it is an owner-started command, but it doesn't set disable-model-invocation: true and the model could start it unasked"
  fi
done

if [[ ! -f "$GUARDED" ]]; then
  fail guard "$GUARDED does not exist"
else
  while read -r gname _; do
    [[ -z "$gname" || "$gname" == \#* ]] && continue
    [[ -f "$AGENTS_DIR/$gname.md" ]] || fail guard "$GUARDED lists '$gname', but $AGENTS_DIR/$gname.md does not exist"
  done < "$GUARDED"
fi

# The flow serves every repo, so no agent, skill or doc may name one repo's tools or words.
if [[ ! -f "$BANNED" ]]; then
  fail banned "$BANNED does not exist"
else
  lists=("$BANNED")
  [[ -f "$PRIVATE_BANNED" ]] && lists+=("$PRIVATE_BANNED")
  banned="$(cat "${lists[@]}" | grep -v -e '^#' -e '^[[:space:]]*$' || true)"
  if [[ -n "$banned" ]]; then
    # grep exits 1 for no match and 2 for an error; only 1 is clean.
    status=0
    scan=("$AGENTS_DIR" "$SKILLS_DIR")
    [[ -d "$DOCS_DIR" ]] && scan+=("$DOCS_DIR")
    hits="$(grep -rniF -e "$banned" "${scan[@]}" 2>&1)" || status=$?
    if (( status == 1 )); then
      :
    elif (( status != 0 )); then
      fail banned "grep failed scanning ${scan[*]} (exit $status): $hits"
    else
      while IFS= read -r hit; do
        fail banned "$hit — a word from ${lists[*]}"
      done <<<"$hits"
    fi
  fi
fi

if (( FAIL == 0 )); then
  echo "OK: agent definitions passed (${#AGENT_FILES[@]} files)"
fi
exit "$FAIL"
