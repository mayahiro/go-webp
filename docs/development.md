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
make compare-lossless ARGS='-runs 1 -mode default -method 4 -corpus ../.local/corpus/production -split holdout -fixtures anonymous-id-1,anonymous-id-2'
```

The report includes decoded RGB error and alpha equality. Ordinary lossless
profiles require exact pixels. Private corpus inputs use anonymous pixel-hash
IDs and report only the corpus SHA-256; source names and paths are not printed.
Paths passed through `ARGS` are resolved from the `benchmarks` directory.
Use `-fixtures` to repeat a tuning measurement for selected generated fixture
names or anonymous private-corpus IDs. The filter is recorded in JSON reports.

## Local Lossy Rate-Distortion Comparison

```sh
make compare-lossy ARGS='-runs 3 -go-mode default'
```

This command requires `cwebp` and `dwebp`. Its schema-version 8 JSON report
contains:

- Quality sweeps for go-webp and cwebp
- Decoded RGB, YUV, alpha, weighted 7x7 luma SSIM, and RGB composite metrics
  over black, white, and 8x8 black-and-white checker backgrounds
- Encoded size and VP8 partition sizes
- One warm-up followed by the requested timed runs, with median, minimum, and
  maximum timing
- SHA-256 of each encoded output and the Go version, GOOS, GOARCH, and
  GOMAXPROCS used for the run
- The nearest sampled cwebp fixture points by encoded size and luma SSIM dB
- Aggregate target-size and quality-matched points interpolated with PCHIP
  inside the measured overlap
- Overall, source-format, and alpha-presence aggregates for nominal-quality and
  quality-matched comparisons
- Fixture-mean and pixel-weighted luma SSIM and RGB/Y/UV/composite PSNR,
  byte-weighted rate totals, and alpha-exact violation counts
- RGB-PSNR and luma-SSIM BD-rate, BD-PSNR, and BD-SSIM when each Pareto curve
  retains at least four measured points

All comparison delta fields use one direction and one percentage reference:

```text
go_minus_cwebp = go-webp - cwebp
go_minus_cwebp_percent = 100 * go_minus_cwebp / cwebp
```

A negative size delta means that go-webp is smaller. A positive PSNR or SSIM
delta means that go-webp has the higher decoded quality metric. Exact metrics
are represented by `null` in samples. A match between two exact metrics has a
delta of `0`; a match with only one exact metric has a `null` delta because the
finite difference is undefined.

Fixture-mean and pixel-weighted PSNR values are calculated from their combined
MSE, not by averaging per-fixture PSNR dB values. The byte-weighted rate is the
ratio of summed encoded bytes, so large outputs contribute proportionally. The
`by_alpha.alpha` aggregate keeps alpha-image regressions visible instead of
diluting them with opaque images.

Nearest sampled matches remain in each fixture record as directly measured
evidence. The aggregate rate-distortion section uses shape-preserving piecewise
cubic Hermite interpolation (PCHIP) over encoded bytes per pixel. It reports
target-size and luma-SSIM-matched points only inside the common measured range
and never extrapolates. Bjontegaard integration uses the same PCHIP curves and
the natural logarithm of rate. A negative BD-rate means go-webp needs fewer
bytes at equivalent quality; positive BD-PSNR or BD-SSIM means go-webp has the
higher quality at equivalent rate.

`-runs` controls timed runs and does not include the single warm-up. Every
timed output must match the warm-up output byte-for-byte; report generation
fails if an encoder is non-deterministic. Aggregate timing fields sum the
per-fixture medians. Output SHA-256 values identify the deterministic encoded
bytes without embedding those bytes in the JSON report.

A private local corpus can be selected with `-corpus` and `-split`. Source
names and paths are omitted from the report. Keep private inputs and reports
outside Git. Paths passed through `ARGS` are resolved from the `benchmarks`
directory, so use an absolute path or prefix repository-relative paths with
`../`.

go-webp timing covers only the in-process `Encode` call. cwebp timing includes
process startup, PNG decoding, encoding, and output writing, so cross-encoder
timing is not an encoder-core ranking. The report records both timing totals
but does not calculate a ratio between these unequal scopes.
