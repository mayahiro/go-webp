# Benchmarks

This document records reproducible development measurements for the current
encoder. The numbers are not portable performance guarantees.

## Environment

- Date: 2026-07-10
- OS: darwin
- Architecture: arm64
- CPU: Apple M1 Max
- Go: 1.26.5
- cwebp and dwebp: 1.6.0
- libsharpyuv: 0.4.2

go-webp timings cover the in-process `Encode` call. cwebp timings include
process startup, PNG decoding, encoding, and output writing. Compare sizes and
decoded quality directly, but treat cross-encoder timing as an orientation
signal rather than an encoder-core ranking.

## Commands

Generate the public, deterministic PNG fixture corpus and its manifest:

```sh
go run ./scripts/generate_benchmark_fixtures -out .local/fixtures/public
```

The generated files stay outside Git. The manifest records each fixture's
category, dimensions, alpha presence, normalized RGBA pixel SHA-256, and PNG
SHA-256. All pixels come from repository code under the project license; no
third-party image assets are used. The comparison commands below use the same
six fixture definitions directly. `photo512` is a synthetic high-frequency
texture, not a substitute for a natural-photo corpus.

```sh
go run ./scripts/compare_lossless_libwebp -runs 3 -mode default -method 4
go run ./scripts/compare_lossless_libwebp -runs 3 -mode best -method 6
go run ./scripts/compare_lossless_libwebp -runs 3 -mode near-lossless -quality 75 -method 4
```

```sh
go run ./scripts/compare_lossy_libwebp \
  -runs 3 \
  -qualities 1,5,10,25,40,50,60,70,75,80,85,90,95,100 \
  -method 4 \
  -go-mode default \
  -json report.json

go run ./scripts/compare_lossy_libwebp \
  -runs 3 -qualities 75 -method 4 -go-mode best -json best-report.json
```

The current lossy JSON schema is version 2. It records weighted 7x7 Y SSIM and
matches sampled quality points by Y SSIM dB. It can also read an anonymous
local corpus through `-corpus` and `-split`. The historical quality-sweep table
below was recorded with schema version 1 and retains its original RGB PSNR
matching so that the published measurements are not reinterpreted.

```sh
go test . -run '^$' \
  -bench 'BenchmarkEncodeModeProfiles/(Gradient128|UI256|Palette256)/(Fast|Balanced|BestCompression|LowMemory|Auto|NearLossless75|LossyQ75)$' \
  -benchmem -benchtime=3x -count=1
```

Every comparison output was decoded with `dwebp`. Lossless outputs were checked
for exact pixels. Near-lossless and lossy outputs were measured against the
source, including alpha.

## Lossless Comparison

Default go-webp is compared with `cwebp -lossless -m 4`.

| Fixture | Go bytes | Go time | cwebp bytes | cwebp time |
| --- | ---: | ---: | ---: | ---: |
| `gradient128` | 62 | 12.875 ms | 72 | 17.871 ms |
| `ui256` | 1,572 | 53.028 ms | 1,472 | 7.820 ms |
| `flat128` | 32 | 3.329 ms | 44 | 4.374 ms |
| `palette256` | 876 | 26.098 ms | 886 | 8.589 ms |
| `alpha128` | 428 | 15.795 ms | 398 | 21.880 ms |
| `photo512` | 27,108 | 136.429 ms | 108,242 | 156.408 ms |

`ModeBestCompression` is compared with `cwebp -lossless -m 6`.

| Fixture | Go bytes | Go time | cwebp bytes | cwebp time |
| --- | ---: | ---: | ---: | ---: |
| `gradient128` | 62 | 18.324 ms | 72 | 47.920 ms |
| `ui256` | 1,008 | 177.195 ms | 1,472 | 8.602 ms |
| `flat128` | 32 | 8.116 ms | 44 | 4.464 ms |
| `palette256` | 876 | 118.171 ms | 836 | 11.511 ms |
| `alpha128` | 412 | 28.290 ms | 398 | 57.084 ms |
| `photo512` | 26,856 | 570.814 ms | 108,242 | 609.069 ms |

The pure Go encoder is already smaller on the gradient, flat, and photo-like
fixtures. Best compression also beats cwebp on the UI fixture. cwebp remains
smaller and usually much faster on the palette and alpha fixtures.

## Near-Lossless Q75 Comparison

Both encoders use lossless WebP with near-lossless quality 75. The maximum RGB
channel error is 2 for every changed fixture.

| Fixture | Go bytes / time | Go RGB MAE | Go alpha exact | cwebp bytes / time | cwebp RGB MAE | cwebp alpha exact |
| --- | ---: | ---: | --- | ---: | ---: | --- |
| `gradient128` | 1,298 / 15.263 ms | 0.9627 | yes | 2,210 / 26.184 ms | 0.6977 | yes |
| `ui256` | 2,752 / 22.492 ms | 0.2158 | yes | 1,472 / 7.285 ms | 0.0000 | yes |
| `flat128` | 32 / 3.863 ms | 0.0000 | yes | 44 / 4.082 ms | 0.0000 | yes |
| `palette256` | 2,304 / 38.806 ms | 0.9639 | yes | 886 / 8.502 ms | 0.0000 | yes |
| `alpha128` | 2,342 / 16.308 ms | 0.9615 | yes | 5,048 / 29.414 ms | 0.8417 | no |
| `photo512` | 169,162 / 456.544 ms | 0.9857 | yes | 198,950 / 243.382 ms | 0.9903 | yes |

go-webp preserves alpha by contract. The cwebp alpha fixture changes alpha at
this setting. cwebp is substantially smaller on unchanged low-color UI and
palette fixtures, while go-webp is smaller on gradient, flat, alpha, and
photo-like fixtures.

## Lossy Q75 Comparison

The table compares the Default and BestCompression profiles with
`cwebp -q 75 -m 4`. RGB PSNR is measured after `dwebp` decoding.

| Fixture | Default bytes / PSNR / time | Best bytes / PSNR / time | cwebp bytes / PSNR / time |
| --- | ---: | ---: | ---: |
| `gradient128` | 3,958 / 22.714 dB / 1.977 ms | 3,544 / 22.894 dB / 19.434 ms | 3,334 / 22.619 dB / 5.408 ms |
| `ui256` | 3,268 / 35.651 dB / 6.363 ms | 2,874 / 35.552 dB / 55.032 ms | 3,002 / 35.310 dB / 7.499 ms |
| `flat128` | 84 / 48.131 dB / 1.343 ms | 84 / 48.131 dB / 12.822 ms | 98 / 52.509 dB / 4.051 ms |
| `palette256` | 37,316 / 12.671 dB / 11.243 ms | 40,922 / 12.986 dB / 89.741 ms | 40,586 / 12.965 dB / 12.909 ms |
| `alpha128` | 6,178 / 20.256 dB / 3.703 ms | 5,538 / 20.515 dB / 99.270 ms | 5,520 / 20.363 dB / 8.803 ms |
| `photo512` | 141,366 / 13.219 dB / 49.813 ms | 150,058 / 13.465 dB / 367.382 ms | 148,664 / 13.465 dB / 42.599 ms |

BestCompression is close to cwebp at the same nominal quality on the UI,
palette, alpha, and photo-like fixtures, with equal or higher measured PSNR.
Its broader search is still much slower.

## Lossy Quality Sweep

For each go-webp Q75 point, the first match is the sampled cwebp point with the
nearest encoded size. The second is the point with the nearest RGB PSNR.
Deltas are `cwebp - go-webp`.

| Fixture | Go Q75 bytes / PSNR | Nearest size: cwebp Q / size delta / PSNR delta | Nearest PSNR: cwebp Q / size delta / PSNR delta |
| --- | ---: | ---: | ---: |
| `gradient128` | 3,958 / 22.714 dB | Q80 / -3.03% / +0.138 dB | Q75 / -15.77% / -0.095 dB |
| `ui256` | 3,268 / 35.651 dB | Q80 / -0.86% / +0.026 dB | Q80 / -0.86% / +0.026 dB |
| `flat128` | 84 / 48.131 dB | Q1 / +11.90% / -7.081 dB | Q50 / +47.62% / -0.277 dB |
| `palette256` | 37,316 / 12.671 dB | Q60 / -0.94% / +0.230 dB | Q25 / -25.66% / +0.061 dB |
| `alpha128` | 6,178 / 20.256 dB | Q80 / +0.03% / +0.256 dB | Q70 / -14.08% / +0.037 dB |
| `photo512` | 141,366 / 13.219 dB | Q70 / +2.22% / +0.233 dB | Q25 / -31.00% / +0.014 dB |

The nearest-size results show that nominal quality mapping and local
rate-distortion behavior are now close on five non-flat fixtures. At closely
matched PSNR, cwebp still has a meaningful size advantage on the gradient,
palette, alpha, and photo-like fixtures. UI is within one percent in this
sample.

## Mode Profiles

These Go benchmark rows use three timed iterations. `B/op` is measured heap
allocation. The structural `workspace_est_B` metric printed by the benchmark is
not a measured peak and is intentionally omitted here.

| Fixture | Mode | Time | Encoded bytes | B/op | allocs/op |
| --- | --- | ---: | ---: | ---: | ---: |
| `Gradient128` | Fast | 1.603 ms | 49,294 | 12,552 | 23 |
| `Gradient128` | Balanced | 28.278 ms | 1,160 | 236,594 | 61 |
| `Gradient128` | BestCompression | 32.281 ms | 1,160 | 291,629 | 82 |
| `Gradient128` | LowMemory | 4.295 ms | 10,510 | 16,792 | 28 |
| `UI256` | Fast | 2.838 ms | 7,518 | 45,192 | 17 |
| `UI256` | Balanced | 138.556 ms | 220 | 1,149,784 | 43 |
| `UI256` | BestCompression | 312.314 ms | 212 | 2,247,949 | 66 |
| `UI256` | LowMemory | 5.371 ms | 4,240 | 46,616 | 18 |
| `Palette256` | Fast | 2.561 ms | 32,930 | 11,336 | 18 |
| `Palette256` | Balanced | 27.682 ms | 2,786 | 1,820,664 | 54 |
| `Palette256` | BestCompression | 110.531 ms | 2,778 | 2,929,085 | 78 |
| `Palette256` | LowMemory | 5.564 ms | 32,930 | 11,336 | 18 |

On these fixtures, Auto selected Balanced. NearLossless75 measured 17.787,
32.046, and 58.194 ms for Gradient128, UI256, and Palette256 respectively.
LossyQ75 measured 1.894, 5.316, and 8.350 ms.

The bounded parallel lossless search reduced single-run BestCompression time
from 37.46 to 33.01 ms on Gradient128, 328.36 to 306.51 ms on UI256, and
130.43 to 108.61 ms on Palette256 while preserving byte-identical output.
The associated B/op increase was 27-33 KiB. Alpha optimal parsing and parallel
frame analysis reduced the Best alpha fixture from about 462 to 99 ms while
keeping its 5,538-byte output.

## Interpretation and Remaining Gaps

- Lossless photo-like compression is substantially smaller than cwebp on this
  synthetic fixture, but structured UI and palette speed remains behind cwebp
- Default lossy rate-distortion is close by encoded size; cwebp still wins
  several matched-PSNR comparisons
- BestCompression closes same-quality size and PSNR gaps but spends much more
  CPU, especially on alpha and photo-like inputs
- LowMemory keeps heap allocation small by avoiding full-frame source and token
  buffers, at the cost of larger output or repeated computation
- Results use generated fixtures and one machine; real image corpora and peak
  RSS measurements remain valuable follow-up work
