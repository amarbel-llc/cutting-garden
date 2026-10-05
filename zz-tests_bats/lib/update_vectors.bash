#! /usr/bin/env bash

# update_vectors.bash RECORDS_DIR — rewrite whole-document vectors in place from
# the mismatches lib/vectors.bash's assert_vector recorded under RECORDS_DIR
# (cutting-garden#250). Driven by `just test-bats-update-vectors`, which loops
# it to a fixpoint; not part of the gate.
#
# Per recorded call site it replaces the body of the `assert_vector - <<-'EOM'`
# heredoc with the actual output, VERBATIM. It then carries what the rewrite
# implies to the rest of the file, because an edited INPUT document pins the
# same `_base` as the generated document it was edited from:
#
#   - every other copy of a replaced document's envelope (the `---` … `---`
#     block) becomes the new envelope;
#   - every remaining mention of a replaced digest becomes the new digest (an
#     input whose envelope was itself edited, a `BASE_*=` variable, the
#     `@old → @new` an fmt summary names). Old and new digests pair by position.
#
# A site whose text it will not touch is reported, never guessed at: an
# unquoted or argument-form expectation (its envelope and digests are still
# carried), a heredoc that no longer matches what the test compared against,
# one site reached with two different outputs, or output the heredoc form
# cannot carry.
#
# Exit 0: at least one file changed. Exit 3: nothing changed.

set -euo pipefail

records="${1:?usage: update_vectors.bash RECORDS_DIR}"

report() { printf 'update-vectors: %s\n' "$*" >&2; }

leading_tabs() { printf '%s' "${1%%[!$'\t']*}"; }

# drop_trailing_blanks NAME: assert_output ignores trailing empty lines, so
# neither side of a comparison carries any.
drop_trailing_blanks() {
  local -n lines_ref="$1"
  while ((${#lines_ref[@]} > 0)) && [[ -z ${lines_ref[-1]} ]]; do
    unset 'lines_ref[-1]'
  done
}

# read_lines NAME FILE
read_lines() {
  mapfile -t "$1" <"$2"
  drop_trailing_blanks "$1"
}

# envelope_of FILE prints the document's hyphence envelope — its opening `---`
# through the closing one — or nothing when FILE is not an enveloped document.
envelope_of() {
  awk '
    NR == 1 && $0 != "---" { exit }
    { block = block $0 "\n" }
    NR > 1 && $0 == "---" { printf "%s", block; exit }
  ' "$1"
}

# note_digest_replacements EXPECTED ACTUAL pairs the two texts' digests by
# position — the `_base` pin, and any digest an apply or fmt summary names —
# when both carry the same number of them.
note_digest_replacements() {
  local -a old_digests new_digests
  mapfile -t old_digests < <(grep -o 'blake2b256-[a-z0-9]*' "$1")
  mapfile -t new_digests < <(grep -o 'blake2b256-[a-z0-9]*' "$2")
  ((${#old_digests[@]} == ${#new_digests[@]})) || return 0
  local i
  for i in "${!old_digests[@]}"; do
    note_replacement digest "${old_digests[i]}" "${new_digests[i]}"
  done
}

# What one file's rewritten vectors imply for the rest of it: `digest <old>` and
# `envelope <old>` map to their replacements. A poisoned key is one the records
# disagree about, or one that is itself a replacement — carrying it would
# rewrite a document this very pass just produced.
declare -A replacement_of=() poisoned=()

# note_replacement KIND OLD NEW
note_replacement() {
  local key="$1 $2" new="$3"
  [[ -z $2 || -z $new || $2 == "$new" ]] && return 0
  if [[ -n ${replacement_of[$key]:-} && ${replacement_of[$key]} != "$new" ]]; then
    poisoned[$key]=1
  fi
  replacement_of[$key]="$new"
}

poison_chained_replacements() {
  local key other
  for key in "${!replacement_of[@]}"; do
    for other in "${!replacement_of[@]}"; do
      if [[ ${other%% *} == "${key%% *}" && ${replacement_of[$other]} == "${key#* }" ]]; then
        poisoned[$key]=1
      fi
    done
  done
}

heredoc_call="assert_vector[[:space:]]+-[[:space:]]+<<(-?)'([A-Za-z_][A-Za-z0-9_]*)'[[:space:]]*\$"

# rewrite_site LINE RECORD replaces the heredoc opened on LINE of the file held
# in the global array L. Returns 1 (after reporting) when it leaves it alone.
rewrite_site() {
  local line="$1" record="$2"
  local header="${L[line - 1]}"
  if [[ ! $header =~ $heredoc_call ]]; then
    report "$file:$line: not a literal \`assert_vector - <<'DELIM'\` heredoc — only its envelope and digests are carried; update the rest by hand"
    return 1
  fi
  local dash="${BASH_REMATCH[1]}" delimiter="${BASH_REMATCH[2]}"

  local end candidate
  for ((end = line; end < ${#L[@]}; end++)); do
    candidate="${L[end]}"
    [[ -n $dash ]] && candidate="${candidate#"${candidate%%[!$'\t']*}"}"
    [[ $candidate == "$delimiter" ]] && break
  done
  if ((end >= ${#L[@]})); then
    report "$file:$line: no closing $delimiter — left alone"
    return 1
  fi

  local -a body=("${L[@]:line:end-line}") expected actual
  if [[ -n $dash ]]; then
    local i
    for i in "${!body[@]}"; do
      body[i]="${body[i]#"${body[i]%%[!$'\t']*}"}"
    done
  fi
  drop_trailing_blanks body
  read_lines expected "$record/expected"
  if [[ ${body[*]@Q} != "${expected[*]@Q}" ]]; then
    report "$file:$line: the heredoc is not what the test compared against — left alone"
    return 1
  fi

  read_lines actual "$record/actual"
  local indent="" text
  [[ -n $dash ]] && indent="$(leading_tabs "${L[end]}")"
  local -a replacement=()
  for text in "${actual[@]}"; do
    if [[ $text == "$delimiter" || (-n $dash && $text == $'\t'*) ]]; then
      report "$file:$line: the output has a line a <<${dash}'$delimiter' heredoc cannot carry — update it by hand"
      return 1
    fi
    replacement+=("${text:+$indent$text}")
  done

  L=("${L[@]:0:line}" "${replacement[@]}" "${L[@]:end}")
}

# replace_envelopes swaps every remaining copy of an old envelope in L for its
# new one, at the copy's own indentation.
replace_envelopes() {
  local key
  for key in "${!replacement_of[@]}"; do
    [[ $key == "envelope "* && -z ${poisoned[$key]:-} ]] || continue
    local -a old_lines new_lines
    mapfile -t old_lines <<<"${key#envelope }"
    mapfile -t new_lines <<<"${replacement_of[$key]}"
    local i=0 k indent matched
    local -a rebuilt=()
    while ((i < ${#L[@]})); do
      matched=0
      if [[ ${L[i]} == *"${old_lines[0]}" ]] && ((i + ${#old_lines[@]} <= ${#L[@]})); then
        indent="${L[i]%%[!$'\t']*}"
        matched=1
        for ((k = 0; matched && k < ${#old_lines[@]}; k++)); do
          [[ ${L[i + k]} == "$indent${old_lines[k]}" ]] || matched=0
        done
      fi
      if ((matched)); then
        for k in "${!new_lines[@]}"; do
          rebuilt+=("$indent${new_lines[k]}")
        done
        ((i += ${#old_lines[@]}))
      else
        rebuilt+=("${L[i]}")
        ((i += 1))
      fi
    done
    L=("${rebuilt[@]}")
  done
}

replace_digests() {
  local key i
  for key in "${!replacement_of[@]}"; do
    [[ $key == "digest "* && -z ${poisoned[$key]:-} ]] || continue
    for i in "${!L[@]}"; do
      L[i]="${L[i]//"${key#digest }"/"${replacement_of[$key]}"}"
    done
  done
}

declare -A site_record=() site_conflict=() file_seen=()
for record in "$records"/record.*; do
  [[ -d $record ]] || continue
  key="$(<"$record/file"):$(<"$record/line")"
  file_seen["${key%:*}"]=1
  if [[ -z ${site_record[$key]:-} ]]; then
    site_record[$key]="$record"
  elif ! cmp -s "$record/actual" "${site_record[$key]}/actual"; then
    site_conflict[$key]=1
  fi
done

files_changed=0
for file in "${!file_seen[@]}"; do
  mapfile -t L <"$file"
  before="${L[*]@Q}"
  replacement_of=()
  poisoned=()
  sites=0

  mapfile -t lines < <(
    for key in "${!site_record[@]}"; do
      if [[ ${key%:*} == "$file" ]]; then
        printf '%s\n' "${key##*:}"
      fi
    done | sort -rn
  )
  for line in "${lines[@]}"; do
    key="$file:$line"
    record="${site_record[$key]}"
    if [[ -n ${site_conflict[$key]:-} ]]; then
      report "$file:$line: reached with different outputs, so it is not one vector — left alone"
      continue
    fi
    if rewrite_site "$line" "$record"; then
      sites=$((sites + 1))
    fi
    note_digest_replacements "$record/expected" "$record/actual"
    note_replacement envelope \
      "$(envelope_of "$record/expected")" "$(envelope_of "$record/actual")"
  done

  poison_chained_replacements
  for key in "${!poisoned[@]}"; do
    if [[ $key == "digest "* ]]; then
      report "$file: \`_base\` ${key#digest } has no single replacement — its other mentions are left alone"
    fi
  done
  replace_envelopes
  replace_digests

  if [[ ${L[*]@Q} != "$before" ]]; then
    printf '%s\n' "${L[@]}" >"$file"
    files_changed=$((files_changed + 1))
    report "$file: rewrote $sites vector(s)"
  fi
done

((files_changed > 0)) || exit 3
