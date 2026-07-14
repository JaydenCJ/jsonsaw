# Paths and the memory model

This document specifies the path language shared by `--path` on every
subcommand, and the exact memory guarantee jsonsaw makes.

## Path syntax

A path addresses one value inside a JSON document. The grammar is
deliberately tiny — no wildcards, no filters, no recursion — because
navigation must complete in a single forward pass over a stream.

```
path     = [ "." ] segment { separator segment }
segment  = key | index
key      = bare | quoted
index    = "[" digits "]"
```

| Form | Example | Meaning |
|---|---|---|
| Bare key | `data.users` | descend into object key `data`, then `users` |
| Index | `results[0].rows` | first element of the `results` array, then key `rows` |
| Chained indexes | `grid[2][10]` | row 2, column 10 of a nested array |
| Root array index | `[0].items` | for documents whose root is an array |
| Quoted key | `payload."weird.key"` | keys containing `.`, `[`, `]`, or `"` |
| Root | `` (empty) or `.` | the document's top-level value |

Rules worth knowing:

- **Bare digits are object keys.** `data.0` addresses the key `"0"`;
  array indexing is always written with brackets (`data[0]`). This is
  what makes documents like `{"0": {...}}` addressable at all.
- **Quoted segments** support exactly two escapes, `\"` and `\\`.
  Everything else inside the quotes is literal.
- **A single leading dot is tolerated** (`.data.users` ≡ `data.users`),
  so paths copied from `jsonsaw paths` output work unedited. The `[]`
  suffix in `paths` output marks the sampled first element of an array
  and is not itself path syntax — drop it when addressing.
- **Key matching is on decoded strings.** A document that spells a key
  `"café"` matches the path segment `café`.
- Non-ASCII keys need no quoting: `データ.利用者` is a valid path.

Mistyped paths (empty segments, negative indexes, unterminated quotes)
are rejected with exit code 2 before any input is read. Paths that parse
but do not resolve against the document — a missing key, an index out of
range, indexing into an object — fail with exit code 1 and name the
exact prefix that failed:

```text
jsonsaw: path data.users[5]: index 5 out of range (array has 3 elements)
```

## The memory guarantee

jsonsaw never materializes a document tree. Every subcommand is built on
one incremental tokenizer that reads the input in a single forward pass,
so peak memory is:

```
O(nesting depth + largest single token)
```

— independent of document size. In practice that means a flat ~10 MiB
process for a 187 MB export with two million records, and the same
~10 MiB for a 50 GB one. The two components of the bound:

- **Nesting depth** costs one byte of state per open container, capped
  at 10,000 levels so a hostile stream of open brackets cannot grow the
  stack without bound.
- **Largest single token** means one string or number literal. jsonsaw
  holds exactly one scalar token in memory at a time; elements
  themselves are never buffered — they flow token-by-token from the
  reader to the writer, so a 100-byte record and a 100 MB record cost
  the same.

Two deliberate policy notes:

- **Tail validation.** After the target array closes, `split` keeps
  reading to end of input, so a truncated or corrupt document tail still
  fails loudly. `--limit N` is the exception: it stops reading the
  moment the limit is hit (that is the point of a limit on a 50 GB
  file), so corruption after the window is intentionally not seen.
- **Byte fidelity.** String escapes and number spellings are copied
  verbatim, never re-encoded: `1e2` stays `1e2`, `é` stays
  `é`. Split-then-join output is byte-identical to a compact
  rendering of the source array, which keeps checksums meaningful.

## JSONL details

`split` emits one element per line. Valid JSON strings cannot contain a
raw newline (it must be escaped as `\n`), so every element is exactly
one line by construction — no quoting layer needed.

`join` reads the superset: any whitespace-separated sequence of JSON
values. Blank lines, indented lines, and multiple values on one line are
all accepted, and each source file is tokenized independently so errors
report `file: line N`.
