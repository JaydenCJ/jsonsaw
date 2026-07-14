#!/usr/bin/env bash
# End-to-end smoke test for jsonsaw: builds the binary, saws a realistic
# API export apart in a temp dir, welds it back together, and asserts on
# real CLI output, byte-identical round trips, and exit codes. No network,
# idempotent, finishes in seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/jsonsaw"

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/jsonsaw) || fail "go build failed"

echo "2. version matches manifest"
"$BIN" --version | grep -qx "jsonsaw 0.1.0" || fail "--version mismatch"

echo "3. generate a realistic paged API export"
cd "$WORKDIR"
{
  printf '{"meta":{"source":"api export","page":1},"data":{"users":['
  for i in $(seq 1 500); do
    [ "$i" -gt 1 ] && printf ','
    printf '{"id":%d,"name":"user-%03d","score":%d.5,"tags":["a","b"]}' "$i" "$i" "$((i % 100))"
  done
  printf ']},"cursor":null}'
} > export.json
[ -s export.json ] || fail "export.json not generated"

echo "4. paths finds the array without loading the document"
"$BIN" paths --depth 2 export.json > paths.txt
grep -q '\.data\.users *array\[500\]' paths.txt || fail "paths did not report .data.users array[500]"
grep -q '\.meta *object{2}' paths.txt || fail "paths did not report .meta"

echo "5. count agrees"
[ "$("$BIN" count --path data.users export.json)" = "500" ] || fail "count != 500"

echo "6. split to JSONL, one element per line"
"$BIN" split --path data.users --out users.jsonl export.json 2> summary.txt
grep -q "jsonsaw split: 500 elements written" summary.txt || fail "split summary missing"
[ "$(wc -l < users.jsonl)" -eq 500 ] || fail "expected 500 JSONL lines"
head -1 users.jsonl | grep -qx '{"id":1,"name":"user-001","score":1.5,"tags":\["a","b"\]}' || fail "first JSONL line wrong"

echo "7. skip/limit carve a window"
"$BIN" split --path data.users --skip 10 --limit 2 --quiet export.json > window.jsonl
[ "$(wc -l < window.jsonl)" -eq 2 ] || fail "window should hold 2 elements"
head -1 window.jsonl | grep -q '"id":11' || fail "window starts at the wrong element"

echo "8. chunked split shards into part files"
"$BIN" split --path data.users --chunk 150 --out parts --quiet export.json
[ "$(ls parts | wc -l)" -eq 4 ] || fail "expected 4 part files"
[ "$(wc -l < parts/part-00000.jsonl)" -eq 150 ] || fail "part 0 should hold 150 elements"
[ "$(wc -l < parts/part-00003.jsonl)" -eq 50 ] || fail "last part should hold the 50-element remainder"

echo "9. join welds the shards back, byte-identical to the unchunked split"
"$BIN" join --quiet parts/part-*.jsonl > rejoined_flat.json
"$BIN" split --quiet rejoined_flat.json | cmp -s - users.jsonl || fail "shard round trip not byte-identical"

echo "10. join --path rebuilds the nested envelope"
"$BIN" join --path data.users --quiet users.jsonl > rejoined.json
grep -q '^{"data":{"users":\[' rejoined.json || fail "wrapper structure missing"
"$BIN" count --path data.users rejoined.json | grep -qx "500" || fail "rejoined count != 500"

echo "11. pretty join is valid and indented"
"$BIN" join --path data.users --pretty --quiet users.jsonl > pretty.json
grep -q '^  "data": {$' pretty.json || fail "pretty indentation missing"
"$BIN" count --path data.users pretty.json | grep -qx "500" || fail "pretty output not re-parseable"

echo "12. data errors exit 1 with positions"
if echo '[1,2,' | "$BIN" split >/dev/null 2>err.txt; then
  fail "truncated input should fail"
fi
[ "$(echo '[1,2,' | "$BIN" split >/dev/null 2>&1; echo $?)" -eq 1 ] || fail "bad JSON should exit 1"
grep -Eq "line [0-9]+, col [0-9]+" err.txt || fail "error should carry a position"
[ "$("$BIN" count --path nope export.json >/dev/null 2>&1; echo $?)" -eq 1 ] || fail "bad path should exit 1"

echo "13. usage errors exit 2"
[ "$("$BIN" split --chunk 5 export.json >/dev/null 2>&1; echo $?)" -eq 2 ] || fail "--chunk without --out should exit 2"
[ "$("$BIN" frobnicate >/dev/null 2>&1; echo $?)" -eq 2 ] || fail "unknown command should exit 2"

echo "14. missing files exit 3"
[ "$("$BIN" split no-such-file.json >/dev/null 2>&1; echo $?)" -eq 3 ] || fail "missing file should exit 3"

echo "SMOKE OK"
