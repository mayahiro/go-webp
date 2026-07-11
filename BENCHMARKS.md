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
| Gradient 128x128 | 1 | 4.425 ms | 866 | 104,618 | 20 |
| Gradient 128x128 | 50 | 4.347 ms | 2,768 | 109,162 | 20 |
| Gradient 128x128 | 75 | 5.211 ms | 3,506 | 112,234 | 20 |
| Gradient 128x128 | 90 | 11.372 ms | 5,284 | 171,178 | 24 |
| Gradient 128x128 | 100 | 11.889 ms | 8,306 | 182,954 | 21 |
| UI 256x256 | 75 | 18.278 ms | 2,906 | 392,112 | 21 |
| Flat 128x128 | 75 | 4.155 ms | 84 | 107,706 | 19 |
| Palette 256x256 | 75 | 26.833 ms | 39,042 | 480,512 | 23 |
| Alpha 128x128 | 75 | 6.892 ms | 5,582 | 144,650 | 33 |
| Photo-like 512x512 | 75 | 117.721 ms | 142,078 | 1,836,608 | 22 |

The photo-like fixture is deterministic synthetic content and is not a
substitute for a natural-photo corpus.

## Lossless

| Fixture | Time | encoded_B | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Gradient 128x128 | 9.031 ms | 62 | 78,482 | 54 |
| UI 256x256 | 52.672 ms | 1,572 | 1,852,280 | 52 |
| Flat 128x128 | 3.232 ms | 32 | 8,122 | 13 |
| Palette 256x256 | 25.566 ms | 876 | 1,257,448 | 45 |
| Alpha 128x128 | 15.971 ms | 428 | 99,666 | 60 |
| Photo-like 512x512 | 136.537 ms | 27,108 | 7,265,800 | 76 |

## Interpretation

- Q75 lossy encodes for the generated fixtures through 256x256 remain below
  30 ms on this machine; the photo-like 512x512 fixture takes about 118 ms
- High-quality lossy encoding costs more because it enables a broader source
  and mode search
- Lossless performance varies substantially with image structure
- Current lossy latency does not justify architecture-specific assembly in the
  project scope, where encoded size and decoded quality take priority
- Before adding SIMD or assembly, profile-guided work should identify a stable
  transform or pixel-processing hotspot and demonstrate a meaningful end-to-end
  benefit on both amd64 and arm64
