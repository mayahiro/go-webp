# Benchmarks

This document records reproducible local benchmark results for the current
encoder implementation. Treat the numbers as development references, not as
portable performance guarantees.

## Environment

- Date: 2026-06-18, 2026-06-25, and 2026-07-10
- OS: darwin
- Architecture: arm64
- CPU: Apple M1 Max
- Go command: Go 1.26.5 for the 2026-07-10 results; local default Go
  toolchain unless otherwise noted for earlier results

## Commands

```sh
go test ./... -run '^$' -bench 'BenchmarkEncodeLossy(Alpha128|AlphaBands512|AlphaNeighborhood512|Gradient1024|YCbCr512|Paletted512)$' -benchmem -count=3
```

```sh
go test . -run '^$' -bench 'BenchmarkEncodeLosslessSmallFixtures/(Gradient128|UI256|Flat128|Palette256)$' -benchmem -count=5
go test . -run '^$' -bench 'BenchmarkEncodeModeProfiles/(Gradient128|UI256|Palette256)/(Fast|Balanced|BestCompression|LowMemory|Auto|NearLossless75|LossyQ75)$' -benchmem -benchtime=1x
go test . -run '^$' -bench 'BenchmarkEncodeModeLargeProfiles/(UI1024|Palette1024)/(Fast|LowMemory|Auto)$' -benchmem -benchtime=1x
go test . -run '^$' -bench 'BenchmarkEncodeModeHugeProfiles/(Gradient4096|UI4096|Palette4096)/(Fast|LowMemory|Auto)$' -benchmem -benchtime=1x
go run ./scripts/compare_lossless_libwebp -runs 7
```

```sh
go test . -run '^$' -bench 'BenchmarkEncodeLossyGradient1024$' -benchmem -benchtime=10x -cpuprofile /tmp/go-webp-lossy-gradient1024.cpu -memprofile /tmp/go-webp-lossy-gradient1024.mem
go tool pprof -top -nodecount=20 /tmp/go-webp-lossy-gradient1024.cpu
go tool pprof -top -nodecount=20 -alloc_space /tmp/go-webp-lossy-gradient1024.mem
```

## Lossless Results

These lossless numbers are five-run local ranges from the current development
fixtures. Use them to compare local changes, not to claim portable performance.

| Benchmark | Time | Encoded bytes | Encoded/input | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| `BenchmarkEncodeLosslessSmallFixtures/Gradient128` | 28.15-28.29 ms/op | 1,160 | 0.01770 | 613,424-613,567 | 68 |
| `BenchmarkEncodeLosslessSmallFixtures/UI256` | 4.11-4.14 ms/op | 250 | 0.0009537 | 68,200 | 34 |
| `BenchmarkEncodeLosslessSmallFixtures/Flat128` | 3.10-3.13 ms/op | 32 | 0.0004883 | 8,120 | 13 |
| `BenchmarkEncodeLosslessSmallFixtures/Palette256` | 9.32-9.36 ms/op | 3,078 | 0.04692 | 739,320-739,323 | 48 |

## Lossless libwebp Comparison

This comparison uses fixed generated PNG fixtures, `cwebp -lossless`, and
`dwebp` exact pixel verification for both encoders. The command measures
go-webp in-process and measures libwebp through the `cwebp` command, so startup
cost is included for libwebp. Treat the timing numbers as local development
references, not as a portable speed ranking.

- Date: 2026-07-10
- libwebp tools: `cwebp` 1.6.0, `dwebp` 1.6.0
- Command: `go run ./scripts/compare_lossless_libwebp -runs 7 -out <output-dir>`

| Fixture | Encoder | Runs | Encoded bytes | Average encode time |
| --- | --- | ---: | ---: | ---: |
| `gradient128` | `go-webp` | 7 | 62 | 10.098 ms |
| `gradient128` | `libwebp` | 7 | 72 | 17.684 ms |
| `ui256` | `go-webp` | 7 | 2,900 | 7.500 ms |
| `ui256` | `libwebp` | 7 | 1,472 | 7.618 ms |
| `flat128` | `go-webp` | 7 | 32 | 3.109 ms |
| `flat128` | `libwebp` | 7 | 44 | 4.453 ms |
| `palette256` | `go-webp` | 7 | 1,532 | 6.731 ms |
| `palette256` | `libwebp` | 7 | 886 | 8.569 ms |
| `alpha128` | `go-webp` | 7 | 428 | 16.253 ms |
| `alpha128` | `libwebp` | 7 | 398 | 22.933 ms |
| `photo512` | `go-webp` | 7 | 27,108 | 137.681 ms |
| `photo512` | `libwebp` | 7 | 108,242 | 156.989 ms |

## Mode Profile Results

The mode profile benchmark uses the same fixtures with explicit `Options.Mode`
settings. `Fast` and `LowMemory` are expected to trade compression ratio for
less search work. `Auto` is conservative and does not force the fast profile on
these 128-256px fixtures. For `Auto` rows, the benchmark also reports
`auto_mode` and `auto_reason`; the values are the numeric `Mode` and internal
classification category selected by the encoder. The benchmark also reports
`pixels` and `workspace_est_B`. `workspace_est_B` is a rough structural estimate,
not a measured peak RSS. For `NearLossless` rows, the benchmark also reports
`rgb_mae`, `rgb_max_abs`, and `alpha_exact`.

| Fixture | Mode | Time | Encoded bytes | Encoded/input | B/op | allocs/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| `Gradient128` | `Fast` | 0.78 ms/op | 49,294 | 0.7522 | 12,552 | 23 |
| `Gradient128` | `Balanced` | 29.16 ms/op | 1,160 | 0.01770 | 613,432 | 68 |
| `Gradient128` | `BestCompression` | 39.29 ms/op | 1,160 | 0.01770 | 741,896 | 79 |
| `Gradient128` | `LowMemory` | 4.27 ms/op | 10,510 | 0.1604 | 16,792 | 28 |
| `Gradient128` | `Auto` | 28.00 ms/op | 1,160 | 0.01770 | 618,912 | 75 |
| `Gradient128` | `NearLossless75` | 64.41 ms/op | 3,412 | 0.05206 | 1,354,296 | 78 |
| `Gradient128` | `LossyQ75` | 1.89 ms/op | 2,658 | 0.04056 | 107,760 | 14 |
| `UI256` | `Fast` | 2.83 ms/op | 7,518 | 0.02868 | 45,192 | 17 |
| `UI256` | `Balanced` | 4.05 ms/op | 250 | 0.0009537 | 68,200 | 34 |
| `UI256` | `BestCompression` | 59.80 ms/op | 250 | 0.0009537 | 68,232 | 36 |
| `UI256` | `LowMemory` | 2.84 ms/op | 7,518 | 0.02868 | 45,192 | 17 |
| `UI256` | `Auto` | 4.16 ms/op | 250 | 0.0009537 | 69,008 | 37 |
| `UI256` | `NearLossless75` | 30.50 ms/op | 248 | 0.0009460 | 68,224 | 35 |
| `UI256` | `LossyQ75` | 7.38 ms/op | 1,170 | 0.004463 | 379,232 | 18 |
| `Palette256` | `Fast` | 2.67 ms/op | 32,930 | 0.5020 | 11,336 | 18 |
| `Palette256` | `Balanced` | 9.25 ms/op | 3,078 | 0.04692 | 739,320 | 48 |
| `Palette256` | `BestCompression` | 70.41 ms/op | 3,078 | 0.04692 | 739,352 | 50 |
| `Palette256` | `LowMemory` | 2.55 ms/op | 32,930 | 0.5020 | 11,336 | 18 |
| `Palette256` | `Auto` | 9.22 ms/op | 3,078 | 0.04692 | 739,320 | 48 |
| `Palette256` | `NearLossless75` | 27.04 ms/op | 3,088 | 0.04707 | 773,488 | 53 |
| `Palette256` | `LossyQ75` | 7.78 ms/op | 11,850 | 0.1806 | 390,976 | 21 |

## Large Mode Profile Results

These large mode-profile rows are single-run checks for comparing mode tradeoffs
on larger low-color fixtures. Use `B/op` as the Go benchmark allocation signal
and `workspace_est_B` only as a rough internal workspace estimate.

| Fixture | Mode | Time | Encoded bytes | Encoded/input | Workspace estimate | B/op | allocs/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `UI1024` | `Fast` | 42.85 ms/op | 100,240 | 0.02390 | 42,939,216 | 12,344 | 15 |
| `UI1024` | `LowMemory` | 42.22 ms/op | 100,240 | 0.02390 | 42,939,216 | 12,344 | 15 |
| `UI1024` | `Auto` | 136.05 ms/op | 6,762 | 0.001612 | 42,939,216 | 1,035,296 | 47 |
| `Palette1024` | `Fast` | 36.86 ms/op | 524,450 | 0.5001 | 42,939,216 | 11,336 | 18 |
| `Palette1024` | `LowMemory` | 37.05 ms/op | 524,450 | 0.5001 | 42,939,216 | 11,336 | 18 |
| `Palette1024` | `Auto` | 106.15 ms/op | 130,468 | 0.1244 | 42,939,216 | 17,609,952 | 74 |

## Huge Mode Profile Results

These 4096px rows are single-run checks for explicit memory-oriented mode
profiles. `Fast` and `LowMemory` keep benchmark heap allocations low, but
`Fast` can greatly increase output size. `Auto` maps these huge fixtures to
`ModeLowMemory` in this sample. RSS was not recorded in this sandbox because
`/usr/bin/time -l` could not read `kern.clockrate`; use the Go benchmark `B/op`
and memprofile data as the available local evidence.

| Fixture | Mode | Time | Encoded bytes | Encoded/input | Workspace estimate | B/op | allocs/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `Gradient4096` | `Fast` | 777.65 ms/op | 50,331,790 | 0.7500 | 672,084,816 | 12,552 | 23 |
| `Gradient4096` | `LowMemory` | 4,378.39 ms/op | 20,972,622 | 0.3125 | 672,084,816 | 12,712 | 26 |
| `Gradient4096` | `Auto` | 4,387.15 ms/op | 20,972,622 | 0.3125 | 672,084,816 | 13,136 | 29 |
| `UI4096` | `Fast` | 673.20 ms/op | 1,418,030 | 0.02113 | 672,084,816 | 12,344 | 15 |
| `UI4096` | `LowMemory` | 674.92 ms/op | 1,418,030 | 0.02113 | 672,084,816 | 12,344 | 15 |
| `UI4096` | `Auto` | 1,049.20 ms/op | 1,418,030 | 0.02113 | 672,084,816 | 14,552 | 26 |
| `Palette4096` | `Fast` | 647.28 ms/op | 8,388,770 | 0.5000 | 672,084,816 | 11,336 | 18 |
| `Palette4096` | `LowMemory` | 633.99 ms/op | 8,388,770 | 0.5000 | 672,084,816 | 11,336 | 18 |
| `Palette4096` | `Auto` | 1,055.38 ms/op | 8,388,770 | 0.5000 | 672,084,816 | 12,648 | 32 |

## Lossy Results

| Benchmark | Time | Encoded bytes | Encoded/input | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| `BenchmarkEncodeLossyAlpha128` | 3.19-3.30 ms/op | 3,230 | 0.04929 | 116,672-116,687 | 24 |
| `BenchmarkEncodeLossyAlphaBands512` | 46.13-47.07 ms/op | 55,752 | 0.05317 | 1,531,844-1,531,857 | 26 |
| `BenchmarkEncodeLossyAlphaNeighborhood512` | 47.30-48.04 ms/op | 56,290 | 0.05368 | 1,533,508-1,533,512 | 28 |
| `BenchmarkEncodeLossyYCbCr512` | 42.83-43.02 ms/op | 93,220 | 0.2371 | 1,674,512-1,674,524 | 19 |
| `BenchmarkEncodeLossyPaletted512` | 46.82-46.89 ms/op | 124,004 | 0.4712 | 1,856,352-1,856,356 | 23 |
| `BenchmarkEncodeLossyGradient1024` | 134.47-141.60 ms/op | 228,814 | 0.05455 | 6,033,872-6,033,886 | 18 |

The bounded residual buffer keeps encoded output unchanged while reducing the
repeated macroblock work. Compared with the preceding 2026-07-10 local sample,
`BenchmarkEncodeLossyGradient1024` decreased from 237.58-237.97 ms/op to
145.15-146.10 ms/op. Its estimated workspace increased from 2,060,809 bytes to
5,779,977 bytes.

Quantized VP8 coefficients use a fixed `int16` block type after clamping to
`[-2047, 2047]`. An alternating fixed-iteration comparison against the prior
`int` block type measured 145.03-145.79 ms/op versus 151.23-151.77 ms/op on the
same fixture, with unchanged output and heap allocation.

Y16 and chroma prediction buffers use a 4x4 block-major layout, matching their
transform consumers and avoiding repeated extraction from raster-order
macroblock buffers. Four alternating 20-iteration comparisons measured
139.70-141.66 ms/op versus 141.80-147.43 ms/op for the preceding implementation
on `BenchmarkEncodeLossyGradient1024`. Two alternating comparisons also reduced
`Gradient128` from 1.85-1.89 ms/op to 1.78-1.79 ms/op, `YCbCr512` from
44.94-45.17 ms/op to 42.53 ms/op, and `Paletted512` from 48.68-49.01 ms/op to
46.47-47.55 ms/op. Encoded size, proxy quality metrics, and heap allocations
were unchanged.

The lossy benchmark reports internal proxy metrics:

- `encoded_B`: encoded WebP size for one encode
- `encoded_per_input`: encoded size divided by the input byte count used by the benchmark
- `y_psnr_proxy` and `uv_psnr_proxy`: encoder-side pre-loop-filter reconstruction metrics used for relative development checks
- `residual_workspace_B`: estimated bounded quantized-residual buffer size
- `workspace_est_B`: estimated major lossy workspace size, not full heap peak

## Final Lossy pprof Snapshot

For `BenchmarkEncodeLossyGradient1024` with `-benchtime=10x`:

- CPU top: `vp8ResidualBuffer.appendBlock` 250ms flat, `vp8BoolEncoder.writeBit` 130ms flat, `put4` 130ms flat, `vp8BlockBitCostFromDefaultAndNonZeroPtr` 90ms flat and 170ms cumulative, and `vp8LastNonZeroCoeffPtr` 70ms flat
- Allocation top: `newVP8ResidualBuffer` 39.22 MiB, `newVP8EncodeBuffers` 17.90 MiB, `newVP8BoolEncoderWithCapacity` 5.87 MiB, and benchmark fixture `image.NewNRGBA` 4.00 MiB. The profile includes setup plus ten timed encodes

The default lossy encoder keeps full-frame reconstruction buffers and, when it
fits the 32 MiB limit, a quantized-residual buffer. `ModeLowMemory` and images
above that limit use the repeated-pass path without the residual buffer. Row or
tile reconstruction would require prediction boundary changes, especially for
luma4x4 top-right references, so it remains a separate future low-memory
change.
