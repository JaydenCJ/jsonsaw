# jsonsaw examples

Two runnable scripts, no dependencies beyond bash and a built `jsonsaw`
binary (`go build -o jsonsaw ./cmd/jsonsaw` from the repository root).

| Script | What it shows |
|---|---|
| `make-export.sh` | Generates a realistic paged-API export: a metadata envelope around a records array of any size. Useful as input for everything else. |
| `shard-pipeline.sh` | The whole workflow end to end: `paths` to find the array, `split --chunk` to shard it into JSONL part files, plain `grep`/`wc` on a shard, `join --path` to rebuild the envelope, and a `cmp` proving the round trip is byte-identical. |

Quick start:

```bash
go build -o jsonsaw ./cmd/jsonsaw
bash examples/shard-pipeline.sh
```

Both scripts run entirely in a `mktemp -d` sandbox (or write to stdout)
and never touch the network.
