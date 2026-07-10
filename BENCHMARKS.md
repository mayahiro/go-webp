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

The benchmarks generate deterministic fixtures in memory and time only the
in-process `Encode` call. Fixture construction and one untimed validation
encode are excluded. The output buffer is preallocated and reused.

```sh
go test . -run '^$' \
  -bench '^BenchmarkEncodeLossyFixtures$' \
  -benchmem -benchtime=3x -count=3

go test . -run '^$' \
  -bench '^BenchmarkEncodeLosslessSmallFixtures$' \
  -benchmem -benchtime=3x -count=3
```

Each table reports the median of three runs with three timed iterations per
run. `B/op` is measured heap allocation. `encoded_B` is the deterministic
output size for one encode. The benchmarks also print structural workspace
estimates; those estimates are not measured peak RSS and are omitted here.

## Lossy

| Fixture | Quality | Time | encoded_B | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: |
| Gradient 128x128 | 1 | 3.853 ms | 778 | 104,474 | 20 |
| Gradient 128x128 | 50 | 4.306 ms | 2,288 | 108,330 | 20 |
| Gradient 128x128 | 75 | 5.096 ms | 2,770 | 111,178 | 20 |
| Gradient 128x128 | 90 | 11.166 ms | 4,212 | 170,602 | 24 |
| Gradient 128x128 | 100 | 11.558 ms | 6,882 | 180,330 | 21 |
| Gradient 512x512 | 75 | 91.565 ms | 63,694 | 1,582,400 | 21 |
| Photo-like 256x256 | 75 | 24.199 ms | 21,612 | 412,080 | 21 |
| Checker 128x128 | 75 | 4.504 ms | 184 | 107,834 | 19 |
| Line art 256x256 | 75 | 19.315 ms | 6,874 | 396,336 | 21 |
| Flat 128x128 | 75 | 4.162 ms | 86 | 107,722 | 19 |
| Alpha 128x128 | 75 | 5.999 ms | 3,144 | 141,962 | 34 |
| Alpha bands 512x512 | 75 | 97.635 ms | 64,112 | 1,617,856 | 33 |
| Alpha neighborhood 512x512 | 75 | 94.411 ms | 64,650 | 1,619,397 | 35 |
| Color edge 128x128 | 75 | 4.478 ms | 504 | 108,202 | 19 |

The photo-like fixture is deterministic synthetic content and is not a
substitute for a natural-photo corpus.

## Lossless

| Fixture | Time | encoded_B | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Gradient 128x128 | 28.010 ms | 1,160 | 236,594 | 61 |
| Photo-like 256x256 | 83.133 ms | 83,458 | 30,317,922 | 185 |
| UI 256x256 | 137.988 ms | 220 | 1,149,784 | 43 |
| Flat 128x128 | 3.199 ms | 32 | 8,122 | 13 |
| RGBA 256x256 | 1,091.662 ms | 4,546 | 1,963,218 | 159 |
| Gray 256x256 | 277.828 ms | 9,944 | 18,723,016 | 304 |
| Alpha 128x128 | 38.555 ms | 2,684 | 394,594 | 81 |
| Palette 256x256 | 27.786 ms | 2,786 | 1,820,664 | 54 |

## Interpretation

- Representative Q75 lossy encodes remain below 100 ms at 512x512 on this
  machine
- High-quality lossy encoding costs more because it enables a broader source
  and mode search
- Lossless performance varies substantially by image representation and
  structure; RGBA and Gray inputs remain algorithmic optimization targets
- Current lossy latency does not justify architecture-specific assembly in the
  project scope, where encoded size and decoded quality take priority
- Before adding SIMD or assembly, profile-guided work should identify a stable
  transform or pixel-processing hotspot and demonstrate a meaningful end-to-end
  benefit on both amd64 and arm64
