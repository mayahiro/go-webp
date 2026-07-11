# Benchmarks

This document records reproducible go-webp measurements for the current
encoder. The numbers are development references, not portable performance
guarantees.

## Environment

- Date: 2026-07-11
- OS: darwin
- Architecture: arm64
- CPU: Apple M1 Max
- Go: 1.26.5

## Method

The public benchmark suite lives in the separate `benchmarks` module. It uses
the same deterministic generated fixtures as the local libwebp comparisons
and calls go-webp only through its public API. Fixture construction and one
untimed validation encode are excluded. The output buffer is preallocated and
reused.

```sh
make bench-lossy
make bench-lossless
```

Each table reports the median of three runs with three timed iterations per
run. `B/op` is measured heap allocation. `encoded_B` is the deterministic
output size for one encode.

## Lossy

| Fixture | Quality | Time | encoded_B | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| Gradient 128x128 | 1 | 4.125 ms | 866 | 86,186 | 20 |
| Gradient 128x128 | 50 | 4.499 ms | 2,768 | 90,730 | 20 |
| Gradient 128x128 | 75 | 4.997 ms | 3,506 | 93,802 | 20 |
| Gradient 128x128 | 90 | 11.374 ms | 5,284 | 152,746 | 24 |
| Gradient 128x128 | 100 | 12.018 ms | 8,306 | 164,522 | 21 |
| UI 256x256 | 75 | 17.581 ms | 2,906 | 306,096 | 21 |
| Flat 128x128 | 75 | 3.967 ms | 84 | 89,274 | 19 |
| Palette 256x256 | 75 | 26.041 ms | 39,042 | 394,496 | 23 |
| Alpha 128x128 | 75 | 6.085 ms | 5,582 | 125,738 | 32 |
| Photo-like 512x512 | 75 | 115.379 ms | 142,078 | 1,467,968 | 22 |

The photo-like fixture is deterministic synthetic content and is not a
substitute for a natural-photo corpus.

## Lossless

| Fixture | Time | encoded_B | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Gradient 128x128 | 33.042 ms | 58 | 639,960 | 391 |
| UI 256x256 | 80.038 ms | 1,304 | 1,475,074 | 169 |
| Flat 128x128 | 11.988 ms | 32 | 9,389 | 33 |
| Palette 256x256 | 50.284 ms | 756 | 1,342,994 | 157 |
| Alpha 128x128 | 37.094 ms | 368 | 784,128 | 598 |
| Photo-like 512x512 | 811.380 ms | 23,770 | 17,247,688 | 839 |

## Interpretation

- Q75 lossy encodes for the generated fixtures through 256x256 remain below
  30 ms on this machine; the photo-like 512x512 fixture takes about 115 ms
- High-quality lossy encoding costs more because it enables a broader source
  and mode search
- Lossy reconstruction uses a two-macroblock-row ring, reducing the estimated
  1024x1024 reconstruction workspace from about 1.5 MiB to 48 KiB without
  changing the encoded stream
- Lossless performance varies substantially with image structure. The
  photo-like 512x512 fixture takes about 811 ms and allocates about 17.2 MiB
  per encode in this benchmark
- Lossless finalist search reuses encode-scoped token, hash, and dynamic-
  programming workspaces. It also evaluates color-cache sizes in one source
  traversal and may materialize a bounded transformed pixel plane
- The broader transform, optimal-LZ77, color-cache, and histogram search
  favors encoded size over latency; use `ModeFast` or `ModeLowMemory` when
  latency or retained state is more important
- Current lossy latency does not justify architecture-specific assembly in the
  project scope, where encoded size and decoded quality take priority
- Before adding SIMD or assembly, profile-guided work should identify a stable
  transform or pixel-processing hotspot and demonstrate a meaningful end-to-end
  benefit on both amd64 and arm64
