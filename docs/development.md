# Development Guide

[日本語](development_ja.md)

This guide records the public verification and benchmark commands used while
developing go-webp. Private corpora and private benchmark reports are not part
of the repository.

## Requirements

- Go 1.26.0 or later
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
make verify-external ARGS='-decoder dwebp'
make verify-external ARGS='-decoder ximage'
```

The command checks lossless fixtures for exact pixels and lossy fixtures for
bounded RGB error. The generated set includes fully transparent pixels with
non-zero hidden RGB. CI runs libwebp `dwebp` 1.6.0 and
`golang.org/x/image/webp` v0.41.0 as independent jobs and reports each decoder
version. The dwebp archive is downloaded from the official WebM release site
and verified by SHA-256. Decoder upgrades are made separately from encoder
changes so conformance differences remain reviewable.

Container checks use RFC 9649 as their reference. VP8L structure checks use
the WebP Lossless Bitstream Specification and validate the signature, version,
transform uniqueness, Huffman data, token bounds, and complete payload bit
classification. Decoder acceptance alone is not treated as proof of format
conformance.

## Fuzzing

Every ordinary `go test` run executes the committed seed corpus. The public
`Encode` target covers every public mode, qualities from 1 to 100, NRGBA,
RGBA, Gray, YCbCr, and Paletted images, opaque and alpha inputs, odd
dimensions, non-zero origins, padded strides, deterministic output, and
RIFF/VP8L/VP8/VP8X/ALPH structure. Images are bounded to 8x8 so
`ModeBestCompression` remains fast enough for repeated mutation.

Two focused targets cover larger boundaries with the same image types, origins,
and strides. `FuzzEncodeNearLossless` uses dimensions 1, 2, 3, 63, 64, and 65 to
exercise the preprocessing threshold and checks decoded alpha, unchanged border
pixels, and bounded RGB error. `FuzzEncodeLossyMacroblocks` uses dimensions 15,
16, 17, 31, 32, and 33 to check deterministic output and container/VP8 structure
across macroblock boundaries.

Scheduled and manual GitHub Actions runs mutate all four fuzz targets for five
minutes each with two workers and a 15-minute job timeout. Run them locally
with:

```sh
go test . -run '^$' -fuzz '^FuzzEncodePublicAPI$' -fuzztime=5m -parallel=2
go test . -run '^$' -fuzz '^FuzzEncodeNearLossless$' -fuzztime=5m -parallel=2
go test . -run '^$' -fuzz '^FuzzEncodeLossyMacroblocks$' -fuzztime=5m -parallel=2
go test . -run '^$' -fuzz '^FuzzVP8LLiteralPlanRoundTrip$' -fuzztime=5m -parallel=2
```

After fixing a discovered failure, retain its minimized input under
`testdata/fuzz/<target>` so ordinary tests replay it. Writer failure and error
propagation are covered by a separate table-driven test for every public mode.

## Go Benchmarks

Keep the Go toolchain version fixed when comparing benchmark corpus results.
[Go 1.26 changed JPEG decoding](https://go.dev/doc/go1.26#imagejpeg), so the same
JPEG file can produce different pixels than with Go 1.25. Pixel-derived corpus
IDs and train/holdout assignments can therefore change too. Preserve existing
manifests and baselines when upgrading, and verify normalized pixel hashes before
comparing results. To compare encoder behavior across Go versions, use a fixed
set of decoded pixels, such as a canonical PNG corpus.

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
make compare-lossless ARGS='-runs 3 -mode default -quality 75 -method 4'
make compare-lossless ARGS='-runs 3 -mode best -quality 100 -method 6'
make compare-lossless ARGS='-runs 3 -mode near-lossless -quality 75 -method 4'
make compare-lossless ARGS='-runs 3 -mode best -quality 100 -method 6 -corpus ../.local/corpus/production -split validation'
make compare-lossless ARGS='-runs 1 -mode default -quality 75 -method 4 -corpus ../.local/corpus/production -split validation -fixtures anonymous-id-1,anonymous-id-2'
```

Ordinary comparisons invoke cwebp with `-lossless -exact` and explicit `-q`
and `-m` values. The standard comparison is quality 75 and method 4. The
maximum-compression comparison is quality 100 and method 6.

The schema-version 2 report retains the schema-version 1 fields and adds:

- One warm-up followed by the requested timed runs, with median, minimum,
  maximum, and the compatibility average
- SHA-256 of deterministic output, with report generation failing if a timed
  output differs from the warm-up
- The go-webp commit and dirty state, Go version, GOOS, GOARCH, GOMAXPROCS, CPU
  model, and OS version
- Full normalized cwebp arguments, cwebp quality, method, and version, plus the
  dwebp version
- Separate source-origin and cwebp-input formats; cwebp receives a generated
  PNG after Go decodes the source
- Pixel exactness, alpha exactness, encoded size, VP8L layout, and aggregate
  groups by source-origin format and alpha presence

`development` and `validation` are the preferred split names. The legacy
`train` and `holdout` names remain accepted as aliases. Paths passed through
`ARGS` are resolved from the `benchmarks` directory.

Private source names and paths are never stored in reports. Raw per-fixture
reports contain image-derived identifiers and hashes, so they are private
artifacts written with permission `0600` and must remain outside Git. Terminal
output uses ordinal placeholders and does not print fixture IDs or corpus
hashes. Use `-fixtures` only for local targeted measurements; the selected IDs
are retained only in the private JSON report.

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
