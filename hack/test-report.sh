#!/usr/bin/env bash

# Publish the results of the Ginkgo test run to the GitHub Actions job
# summary, and annotate the failing test files.
#
# The JSON report this script reads is written by hack/test.sh, but only
# when running in GitHub Actions.

set -o errexit
set -o nounset
set -o pipefail

# Change directories to the parent directory of the one in which this
# script is located.
cd "$(dirname "${BASH_SOURCE[0]}")/.."

# The file hack/test.sh passes to ginkgo via --json-report.
report_file="${GINKGO_REPORT_FILE:-ginkgo-report.json}"

# A report is only written when the tests actually run, ex. the report is
# missing if the test binary failed to build.
if [ ! -s "${report_file}" ]; then
  printf '==> No Ginkgo report at %s, skipping test report.\n' "${report_file}"
  exit 0
fi

if ! command -v jq >/dev/null 2>&1; then
  printf '==> jq is required to publish the test report, skipping.\n' >&2
  exit 0
fi

# GitHub silently drops annotations past this many errors per step, which is
# why the job summary, and not the annotations, is the complete list.
readonly max_annotations=10

# Annotations are only useful for files inside the checkout.
readonly workspace="${GITHUB_WORKSPACE:-$(pwd)}"

# jq helpers shared by the summary and annotation programs below:
#
#   spec_name  the full name of a spec, ex. "VM > when created > defaults".
#   rel        strips the workspace prefix so paths are repo-relative.
#   inline     flattens a report value to a single, HTML-escaped line.
#   failed     true for the states that represent a failure. Note that
#              "panicked" is not "failed", so both must be checked.
#
# The $prefix and $limit below are jq variables, not shell expansions.
# shellcheck disable=SC2016
readonly jq_helpers='
def spec_name:
  ((.ContainerHierarchyTexts // []) + [.LeafNodeText // ""])
  | map(select(. != ""))
  | join(" > ");

def rel($prefix):
  if startswith($prefix) then .[($prefix | length):] else . end;

def inline($limit):
  tostring
  | gsub("&"; "&amp;")
  | gsub("<"; "&lt;")
  | gsub(">"; "&gt;")
  | gsub("\\|"; "\\|")
  | split("\n")
  | map(gsub("\\s+"; " ") | sub("^ +"; "") | sub(" +$"; ""))
  | map(select(length > 0))
  | join("<br>")
  | .[0:$limit];

def failed:
  .State == "failed" or .State == "panicked" or .State == "timedout";
'

# The job summary. This is the authoritative list of failures, so it lists
# every failing spec along with its location and failure message.
summary="$(
  jq -r --arg ws "${workspace}/" "${jq_helpers}"'
    [ .[] | (.SpecReports // [])[] ] as $specs
    | [ $specs[] | select(failed) ] as $fails
    | [ .[] | select((.SpecialSuiteFailureReasons // []) | length > 0) ] as $problems
    | ($specs | map(select(.State == "passed"))  | length) as $passed
    | ($specs | map(select(.State == "pending")) | length) as $pending
    | ($specs | map(select(.State == "skipped")) | length) as $skipped
    | (
        if ($fails | length) == 0 and ($problems | length) == 0 then
          "## :white_check_mark: Test Report"
        else
          "## :x: Test Report"
        end
      ),
      "",
      "\($passed) passed, \($fails | length) failed, \($pending) pending, \($skipped) skipped.",
      "",
      (
        if ($fails | length) > 0 then
          "| # | Spec | State | Location | Failure |",
          "|---|------|-------|----------|---------|",
          (
            $fails | to_entries[] |
            "| \(.key + 1)"
            + " | \(.value | spec_name | inline(160))"
            + " | \(.value.State)"
            + " | \(.value.Failure.Location.FileName // "?" | rel($ws)):\(.value.Failure.Location.LineNumber // 0)"
            + " | \(.value.Failure.Message // "" | inline(400))"
            + " |"
          )
        else empty
        end
      ),
      (
        if ($problems | length) > 0 then
          "",
          "### Suites that did not run",
          "",
          (
            $problems[] |
            "- `"
            + (if (.SuiteDescription // "") != "" then .SuiteDescription
               else (.SuitePath // "unknown" | rel($ws)) end)
            + "`: "
            + ((.SpecialSuiteFailureReasons // []) | map(inline(400)) | join("; "))
          )
        else empty
        end
      )
  ' "${report_file}"
)"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  printf '%s\n' "${summary}" >>"${GITHUB_STEP_SUMMARY}"
else
  printf '%s\n' "${summary}"
fi

# Annotate each failing spec that points at a file in this checkout. This is
# capped, so stick to the earliest failures -- they are the ones a reader is
# most likely to act on.
jq -r --arg ws "${workspace}/" --argjson max "${max_annotations}" "${jq_helpers}"'
  def esc_data:
    tostring
    | gsub("%"; "%25")
    | gsub("\\r"; "%0D")
    | gsub("\\n"; "%0A");

  def esc_prop:
    esc_data
    | gsub(":"; "%3A")
    | gsub(","; "%2C");

  def annotation_message($limit):
    tostring
    | split("\n")
    | map(gsub("\\s+"; " ") | sub("^ +"; "") | sub(" +$"; ""))
    | map(select(length > 0))
    | join(" ")
    | .[0:$limit];

  [
    .[] | (.SpecReports // [])[]
    | select(failed)
    | select((.Failure.Location.FileName // "") | startswith($ws))
  ]
  | .[0:$max][]
  | "::error"
    + " file=\(.Failure.Location.FileName | rel($ws) | esc_prop)"
    + ",line=\(.Failure.Location.LineNumber // 1)"
    + ",title=\(spec_name | esc_prop)"
    + "::\(.Failure.Message // "test failed" | annotation_message(300) | esc_data)"
' "${report_file}"

# Reporting must never change the outcome of the test run.
exit 0
