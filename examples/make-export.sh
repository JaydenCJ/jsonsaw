#!/usr/bin/env bash
# Generate a realistic paged-API-style export to experiment with:
# a metadata envelope around a large records array. Size is controlled
# by the first argument (number of records, default 10000).
#
#   bash examples/make-export.sh 50000 > export.json
set -euo pipefail

N="${1:-10000}"

printf '{"meta":{"source":"api export","generated_by":"make-export.sh","page":1},"data":{"records":['
for ((i = 1; i <= N; i++)); do
  ((i > 1)) && printf ','
  printf '{"id":%d,"user":"user-%04d","score":%d.%03d,"active":%s,"tags":["batch","demo"]}' \
    "$i" "$((i % 500))" "$((i % 100))" "$((i * 7 % 1000))" "$([ $((i % 3)) -eq 0 ] && echo true || echo false)"
done
printf ']},"cursor":null}\n'
