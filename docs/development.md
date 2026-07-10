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
go test ./...
go vet ./...
go tool goimports -l .
```

## External Decoder Verification

```sh
go run ./scripts/verify_lossless_external
```

The command checks lossless fixtures for exact pixels and lossy fixtures for
bounded RGB error. It prefers `dwebp`, otherwise uses a temporary
`golang.org/x/image/webp` decoder through `go run`, and uses macOS `sips` only
when neither decoder is available.

## Go Benchmarks

```sh
go test . -run '^$' \
  -bench '^BenchmarkEncodeLossyFixtures$' \
  -benchmem -benchtime=3x -count=3

go test . -run '^$' \
  -bench '^BenchmarkEncodeLosslessSmallFixtures$' \
  -benchmem -benchtime=3x -count=3
```

See [Benchmarks](../BENCHMARKS.md) for the measurement environment, current
results, and interpretation.

## Local Lossless Comparison

```sh
go run ./scripts/compare_lossless_libwebp -runs 3 -mode default -method 4
go run ./scripts/compare_lossless_libwebp -runs 3 -mode best -method 6
go run ./scripts/compare_lossless_libwebp -runs 3 -mode near-lossless -quality 75 -method 4
```

The report includes decoded RGB error and alpha equality. Ordinary lossless
profiles require exact pixels.

## Local Lossy Rate-Distortion Comparison

```sh
go run ./scripts/compare_lossy_libwebp \
  -runs 3 \
  -go-mode default \
  -json report.json
```

This command requires `cwebp` and `dwebp`. Its schema-version 3 JSON report
contains:

- Quality sweeps for go-webp and cwebp
- Decoded RGB, YUV, alpha, and weighted 7x7 luma SSIM metrics
- Encoded size and VP8 partition sizes
- Encode timing
- The nearest sampled cwebp points by encoded size and luma SSIM dB

A private local corpus can be selected with `-corpus` and `-split`. Source
names and paths are omitted from the report. Keep private inputs and reports
outside Git.

go-webp timing covers only the in-process `Encode` call. cwebp timing includes
process startup, PNG decoding, encoding, and output writing, so cross-encoder
timing is not an encoder-core ranking.
