# Benchmarks

This document records reproducible local benchmark results for the current
encoder implementation. Treat the numbers as development references, not as
portable performance guarantees.

## Environment

- Date: 2026-06-18 and 2026-06-25
- OS: darwin
- Architecture: arm64
- CPU: Apple M1 Max
- Go command: local default Go toolchain unless otherwise noted

## Commands

```sh
go test ./... -run '^$' -bench 'BenchmarkEncodeLossy(Alpha128|AlphaBands512|AlphaNeighborhood512|Gradient1024|YCbCr512|Paletted512)$' -benchmem -count=3
```

```sh
go test . -run '^$' -bench 'BenchmarkEncodeLosslessSmallFixtures/(Gradient128|UI256|Flat128|Palette256)$' -benchmem -benchtime=1x
go test . -run '^$' -bench 'BenchmarkEncodeModeProfiles/(Gradient128|UI256|Palette256)/(Fast|Balanced|BestCompression|LowMemory|Auto|NearLossless75|LossyQ75)$' -benchmem -benchtime=1x
go test . -run '^$' -bench 'BenchmarkEncodeModeLargeProfiles/(UI1024|Palette1024)/(Fast|LowMemory|Auto)$' -benchmem -benchtime=1x
go test . -run '^$' -bench 'BenchmarkEncodeModeHugeProfiles/(Gradient4096|UI4096|Palette4096)/(Fast|LowMemory|Auto)$' -benchmem -benchtime=1x
go run ./scripts/compare_lossless_libwebp -runs 3
```

```sh
go test . -run '^$' -bench 'BenchmarkEncodeLossyGradient1024$' -benchmem -benchtime=1x -cpuprofile go-webp-lossy-final-gradient1024.cpu -memprofile go-webp-lossy-final-gradient1024.mem
go tool pprof -top -nodecount=20 go-webp-lossy-final-gradient1024.cpu
go tool pprof -top -nodecount=20 -alloc_space go-webp-lossy-final-gradient1024.mem
```

## Lossless Results

These lossless numbers are single-run local references from the current
development fixtures. Use them to compare local changes, not to claim portable
performance.

| Benchmark | Time | Encoded bytes | Encoded/input | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| `BenchmarkEncodeLosslessSmallFixtures/Gradient128` | 54.08 ms/op | 1,160 | 0.01770 | 563,160 | 58 |
| `BenchmarkEncodeLosslessSmallFixtures/UI256` | 38.44 ms/op | 250 | 0.0009537 | 19,032 | 17 |
| `BenchmarkEncodeLosslessSmallFixtures/Flat128` | 3.32 ms/op | 32 | 0.0004883 | 6,000 | 11 |
| `BenchmarkEncodeLosslessSmallFixtures/Palette256` | 30.76 ms/op | 3,122 | 0.04759 | 112,184 | 21 |

## Lossless libwebp Comparison

This comparison uses fixed generated PNG fixtures, `cwebp -lossless`, and
`dwebp` exact pixel verification for both encoders. The command measures
go-webp in-process and measures libwebp through the `cwebp` command, so startup
cost is included for libwebp. Treat the timing numbers as local development
references, not as a portable speed ranking.

- Date: 2026-06-25
- libwebp tools: `cwebp` 1.6.0, `dwebp` 1.6.0
- Command: `go run ./scripts/compare_lossless_libwebp -runs 3 -out <output-dir>`

| Fixture | Encoder | Runs | Encoded bytes | Average encode time |
| --- | --- | ---: | ---: | ---: |
| `gradient128` | `go-webp` | 3 | 62 | 12.424 ms |
| `gradient128` | `libwebp` | 3 | 72 | 18.224 ms |
| `ui256` | `go-webp` | 3 | 2,900 | 45.401 ms |
| `ui256` | `libwebp` | 3 | 1,472 | 8.145 ms |
| `flat128` | `go-webp` | 3 | 32 | 3.217 ms |
| `flat128` | `libwebp` | 3 | 44 | 4.491 ms |
| `palette256` | `go-webp` | 3 | 1,532 | 25.056 ms |
| `palette256` | `libwebp` | 3 | 886 | 8.932 ms |
| `alpha128` | `go-webp` | 3 | 428 | 22.366 ms |
| `alpha128` | `libwebp` | 3 | 398 | 23.316 ms |
| `photo512` | `go-webp` | 3 | 27,108 | 139.963 ms |
| `photo512` | `libwebp` | 3 | 108,242 | 165.130 ms |

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
| `Gradient128` | `Fast` | 0.86 ms/op | 49,294 | 0.7522 | 12,552 | 23 |
| `Gradient128` | `Balanced` | 53.80 ms/op | 1,160 | 0.01770 | 563,160 | 58 |
| `Gradient128` | `BestCompression` | 61.82 ms/op | 1,160 | 0.01770 | 563,160 | 58 |
| `Gradient128` | `LowMemory` | 5.24 ms/op | 10,510 | 0.1604 | 16,792 | 28 |
| `Gradient128` | `Auto` | 54.70 ms/op | 1,160 | 0.01770 | 563,160 | 58 |
| `Gradient128` | `NearLossless75` | 134.17 ms/op | 3,412 | 0.05206 | 1,082,840 | 65 |
| `Gradient128` | `LossyQ75` | 4.78 ms/op | 3,760 | 0.05737 | 46,576 | 13 |
| `UI256` | `Fast` | 4.06 ms/op | 7,518 | 0.02868 | 12,344 | 15 |
| `UI256` | `Balanced` | 28.80 ms/op | 250 | 0.0009537 | 19,032 | 17 |
| `UI256` | `BestCompression` | 43.99 ms/op | 250 | 0.0009537 | 19,032 | 17 |
| `UI256` | `LowMemory` | 14.31 ms/op | 7,518 | 0.02868 | 12,344 | 15 |
| `UI256` | `Auto` | 28.86 ms/op | 250 | 0.0009537 | 19,032 | 17 |
| `UI256` | `NearLossless75` | 39.03 ms/op | 248 | 0.0009460 | 19,056 | 18 |
| `UI256` | `LossyQ75` | 15.98 ms/op | 1,208 | 0.004608 | 150,304 | 17 |
| `Palette256` | `Fast` | 3.09 ms/op | 32,930 | 0.5020 | 11,336 | 18 |
| `Palette256` | `Balanced` | 24.99 ms/op | 3,122 | 0.04759 | 112,184 | 21 |
| `Palette256` | `BestCompression` | 40.84 ms/op | 3,122 | 0.04759 | 112,184 | 21 |
| `Palette256` | `LowMemory` | 14.09 ms/op | 32,930 | 0.5020 | 11,336 | 18 |
| `Palette256` | `Auto` | 25.09 ms/op | 3,122 | 0.04759 | 112,184 | 21 |
| `Palette256` | `NearLossless75` | 67.71 ms/op | 3,146 | 0.04796 | 113,504 | 24 |
| `Palette256` | `LossyQ75` | 19.45 ms/op | 13,542 | 0.2064 | 163,648 | 20 |

## Large Mode Profile Results

These large mode-profile rows are single-run checks for comparing mode tradeoffs
on larger low-color fixtures. Use `B/op` as the Go benchmark allocation signal
and `workspace_est_B` only as a rough internal workspace estimate.

| Fixture | Mode | Time | Encoded bytes | Encoded/input | Workspace estimate | B/op | allocs/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `UI1024` | `Fast` | 45.31 ms/op | 100,240 | 0.02390 | 42,829,200 | 12,344 | 15 |
| `UI1024` | `LowMemory` | 44.92 ms/op | 100,240 | 0.02390 | 42,829,200 | 12,344 | 15 |
| `UI1024` | `Auto` | 314.89 ms/op | 6,762 | 0.001612 | 42,829,200 | 246,592 | 31 |
| `Palette1024` | `Fast` | 37.26 ms/op | 524,450 | 0.5001 | 42,829,200 | 11,336 | 18 |
| `Palette1024` | `LowMemory` | 37.19 ms/op | 524,450 | 0.5001 | 42,829,200 | 11,336 | 18 |
| `Palette1024` | `Auto` | 154.84 ms/op | 130,468 | 0.1244 | 42,829,200 | 19,440 | 45 |

## Huge Mode Profile Results

These 4096px rows are single-run checks for explicit memory-oriented mode
profiles. `Fast` and `LowMemory` keep benchmark heap allocations low, but
`Fast` can greatly increase output size. `Auto` maps these huge fixtures to
`ModeLowMemory` in this sample. RSS was not recorded in this sandbox because
`/usr/bin/time -l` could not read `kern.clockrate`; use the Go benchmark `B/op`
and memprofile data as the available local evidence.

| Fixture | Mode | Time | Encoded bytes | Encoded/input | Workspace estimate | B/op | allocs/op |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `Gradient4096` | `Fast` | 696.46 ms/op | 50,331,790 | 0.7500 | 671,975,280 | 12,552 | 23 |
| `Gradient4096` | `LowMemory` | 4,607.16 ms/op | 20,972,622 | 0.3125 | 671,975,280 | 12,712 | 26 |
| `Gradient4096` | `Auto` | 4,604.31 ms/op | 20,972,622 | 0.3125 | 671,975,280 | 13,136 | 29 |
| `UI4096` | `Fast` | 692.43 ms/op | 1,418,030 | 0.02113 | 671,975,280 | 12,344 | 15 |
| `UI4096` | `LowMemory` | 693.27 ms/op | 1,418,030 | 0.02113 | 671,975,280 | 12,344 | 15 |
| `UI4096` | `Auto` | 1,092.63 ms/op | 1,418,030 | 0.02113 | 671,975,280 | 14,552 | 26 |
| `Palette4096` | `Fast` | 628.31 ms/op | 8,388,770 | 0.5000 | 671,975,280 | 11,336 | 18 |
| `Palette4096` | `LowMemory` | 638.54 ms/op | 8,388,770 | 0.5000 | 671,975,280 | 11,336 | 18 |
| `Palette4096` | `Auto` | 1,070.57 ms/op | 8,388,770 | 0.5000 | 671,975,280 | 12,648 | 32 |

## Lossy Results

| Benchmark | Time | Encoded bytes | Encoded/input | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| `BenchmarkEncodeLossyAlpha128` | 5.12-5.27 ms/op | 4,344 | 0.06628 | 54,260-54,285 | 21 |
| `BenchmarkEncodeLossyAlphaBands512` | 93.05-93.09 ms/op | 86,434 | 0.08243 | 683,754-683,764 | 23 |
| `BenchmarkEncodeLossyAlphaNeighborhood512` | 93.71-95.64 ms/op | 86,978 | 0.08295 | 685,418-685,428 | 25 |
| `BenchmarkEncodeLossyYCbCr512` | 106.79-107.21 ms/op | 136,428 | 0.3470 | 733,200 | 17 |
| `BenchmarkEncodeLossyPaletted512` | 113.38-117.44 ms/op | 184,394 | 0.7007 | 1,184,366 | 22 |
| `BenchmarkEncodeLossyGradient1024` | 377.84-471.03 ms/op | 388,592 | 0.09265 | 2,810,661-2,810,698 | 17-18 |

`BenchmarkEncodeLossyGradient1024` had one slower run in this sample. The
encoded size, workspace estimates, and allocation counts remained stable.

The lossy benchmark reports internal proxy metrics:

- `encoded_B`: encoded WebP size for one encode
- `encoded_per_input`: encoded size divided by the input byte count used by the benchmark
- `y_psnr_proxy` and `uv_psnr_proxy`: encoder-side pre-loop-filter reconstruction metrics used for relative development checks
- `workspace_est_B`: estimated major lossy workspace size, not full heap peak

## Final Lossy pprof Snapshot

For `BenchmarkEncodeLossyGradient1024` with `-benchtime=1x`:

- CPU top: `put4` 280ms flat, `vp8BlockBitCostFromDefaultAndNonZeroPtr` 80ms flat / 100ms cumulative, `inverseDCT4` 50ms flat / 60ms cumulative, `vp8BoolEncoder.writeBit` 40ms flat
- Allocation top: `newVP8EncodeBuffers` 5,955.52 KiB, benchmark fixture `image.NewNRGBA` 4,097.37 KiB, `bytes.growSlice` 1,455.55 KiB, `newVP8BoolEncoderWithCapacity` 809.97 KiB

The default lossy encoder keeps full-frame reconstruction buffers. Row or tile
processing would require prediction boundary changes, especially for luma4x4
top-right references, so it is deferred to a future explicit low-memory mode
instead of being applied to the default mode.
