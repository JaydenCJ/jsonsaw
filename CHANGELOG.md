# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- Incremental RFC 8259 tokenizer with O(nesting depth + largest single
  token) memory at any document size: strict number/escape grammar,
  leading-zero and trailing-comma rejection, unescaped-control-character
  detection, 1-based line/column positions on every error, UTF-8 BOM
  tolerance, a 10,000-level nesting cap against hostile inputs, and a
  sequence mode that reads JSONL without ever buffering a line.
- `split` subcommand: stream the array at `--path` into JSONL with
  byte-faithful elements (escapes and number spellings preserved
  verbatim), `--skip`/`--limit` windows, `--chunk N --out DIR` sharding
  into `part-00000.jsonl`-style files with a configurable `--prefix`,
  and whole-document tail validation (skipped deliberately when
  `--limit` cuts the read short).
- `join` subcommand: weld JSONL (or any whitespace-separated value
  sequence, from stdin or many files in order) back into a JSON array —
  compact or `--pretty` with configurable `--indent`, optionally nested
  under object keys with `--path`, with errors attributed to
  `file: line N`.
- `count` subcommand: exact element count of the array at `--path` in
  one pass and constant memory.
- `paths` subcommand: one-pass shape report (`.data.records array[2000000]`,
  first-element sampling as `path[]`, exact key/element counts, text or
  JSON output) so the right `--path` can be found without loading
  anything.
- Path language shared by all subcommands: dot-separated keys, bracketed
  indexes, quoted segments for keys containing dots, decoded-string key
  matching, and errors naming the exact prefix that failed
  (`docs/paths.md`).
- Documented exit codes (0 ok, 1 invalid input or path, 2 usage,
  3 I/O) and `--quiet` summaries on stderr, keeping stdout pure data.
- Runnable examples (`examples/make-export.sh`,
  `examples/shard-pipeline.sh`) and a path/memory-model reference
  (`docs/paths.md`).
- 91 deterministic offline tests (unit + in-process CLI integration
  against real temp-dir files) and `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/jsonsaw/releases/tag/v0.1.0
