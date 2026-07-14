# Contributing to jsonsaw

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22; nothing else — the tool and its tests are pure
standard library and never touch the network.

```bash
git clone https://github.com/JaydenCJ/jsonsaw && cd jsonsaw
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary, generates a realistic paged API
export in a temp dir, saws it apart and welds it back, and asserts on
real CLI output, byte-identical round trips, and every documented exit
code; it must finish by printing `SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (91 deterministic tests, no network).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (the tokenizer, path parser, and engines take readers and
   writers — only the CLI layer touches os.Stdin, files, or exit codes).

## Ground rules

- Keep dependencies at zero; adding one needs strong justification in
  the PR.
- No network calls, ever, and no telemetry — jsonsaw only reads the
  input you point it at and writes where you tell it to.
- The memory bound is a contract: nothing may buffer per element or per
  document. If a change needs more than O(depth + one token), it needs a
  design discussion first.
- Byte fidelity is a contract too: string escapes and number spellings
  pass through verbatim. A change that re-encodes values will be
  rejected — checksums over split output must keep matching the source.
- Code comments and doc comments are written in English.

## Reporting bugs

Include the output of `jsonsaw version`, the full command you ran, and
the smallest input that reproduces the problem — for parser issues that
is usually a one-line document plus the reported `line N, col M`
position. For big-file issues, `jsonsaw paths --depth 2` output usually
describes the shape well enough to synthesize a repro.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
