#!/usr/bin/env bash
# The full workflow on a generated export: discover the array, shard it
# into fixed-size JSONL part files, process one shard with ordinary line
# tools, and weld everything back into the original envelope shape.
#
# Run from the repository root:
#   go build -o jsonsaw ./cmd/jsonsaw
#   bash examples/shard-pipeline.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
JSONSAW="$ROOT/jsonsaw"
[ -x "$JSONSAW" ] || { echo "build first: go build -o jsonsaw ./cmd/jsonsaw" >&2; exit 1; }

WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT
cd "$WORKDIR"

echo "# 1. generate a 20k-record export"
bash "$ROOT/examples/make-export.sh" 20000 > export.json

echo "# 2. where is the array? (constant memory, one pass)"
"$JSONSAW" paths --depth 2 export.json

echo
echo "# 3. shard it: 5k records per part file"
"$JSONSAW" split --path data.records --chunk 5000 --out shards export.json
ls shards

echo
echo "# 4. JSONL is line-tool territory: grep one shard, count survivors"
grep '"active":true' shards/part-00002.jsonl | wc -l

echo
echo "# 5. weld all shards back into the envelope shape"
"$JSONSAW" join --path data.records shards/part-*.jsonl > rejoined.json
"$JSONSAW" count --path data.records rejoined.json

echo
echo "# 6. prove the round trip is lossless (byte-identical JSONL)"
"$JSONSAW" split --path data.records --quiet export.json > a.jsonl
"$JSONSAW" split --path data.records --quiet rejoined.json > b.jsonl
cmp a.jsonl b.jsonl && echo "round trip: byte-identical"
