# Development Guide

[日本語](development_ja.md)

This guide records the public verification and benchmark commands used while
developing go-webp. Private corpora and private benchmark reports are not part
of the repository.

## Requirements

- Go 1.25.0 or later
- `cwebp` and `dwebp` only for optional libwebp comparison and external decode
  checks

## Standard Verification

```sh
make check
```

Development tool dependencies are pinned in the separate nested module under
`tools`, while the public performance suite and comparison commands live in
the `benchmarks` module. `make check` tests and vets both code modules and runs
the formatting check. The `tools` module provides `goimports` for formatting
and `pprof` for local profile analysis.

## External Decoder Verification

```sh
make verify-external
```

The command checks lossless fixtures for exact pixels and lossy fixtures for
bounded RGB error. It prefers `dwebp`, otherwise uses a temporary
`golang.org/x/image/webp` decoder through `go run`, and uses macOS `sips` only
when neither decoder is available.

## Go Benchmarks

```sh
make bench-lossy
make bench-lossless
```

See [Benchmarks](../BENCHMARKS.md) for the measurement environment, current
results, and interpretation.

## Fixture and Corpus Helpers

```sh
make generate-fixtures
make index-corpus
```

Both commands write to the Git-ignored `.local` directory by default. The
fixture command generates the deterministic public PNG set and manifest. The
corpus command anonymously indexes images under `.local/corpus/production`.

## Local Lossless Comparison

```sh
make compare-lossless ARGS='-runs 3 -mode default -method 4'
make compare-lossless ARGS='-runs 3 -mode best -method 6'
make compare-lossless ARGS='-runs 3 -mode near-lossless -quality 75 -method 4'
make compare-lossless ARGS='-runs 1 -mode best -method 6 -corpus ../.local/corpus/production -split holdout'
```

The report includes decoded RGB error and alpha equality. Ordinary lossless
profiles require exact pixels. Private corpus inputs use anonymous pixel-hash
IDs and report only the corpus SHA-256; source names and paths are not printed.
Paths passed through `ARGS` are resolved from the `benchmarks` directory.

## Local Lossy Rate-Distortion Comparison

```sh
make compare-lossy ARGS='-runs 3 -go-mode default'
```

This command requires `cwebp` and `dwebp`. Its schema-version 4 JSON report
contains:

- Quality sweeps for go-webp and cwebp
- Decoded RGB, YUV, alpha, and weighted 7x7 luma SSIM metrics
- Encoded size and VP8 partition sizes
- Encode timing
- The nearest sampled cwebp points by encoded size and luma SSIM dB
- Aggregate nominal-quality and quality-matched size and quality summaries

A private local corpus can be selected with `-corpus` and `-split`. Source
names and paths are omitted from the report. Keep private inputs and reports
outside Git. Paths passed through `ARGS` are resolved from the `benchmarks`
directory, so use an absolute path or prefix repository-relative paths with
`../`.

go-webp timing covers only the in-process `Encode` call. cwebp timing includes
process startup, PNG decoding, encoding, and output writing, so cross-encoder
timing is not an encoder-core ranking.
