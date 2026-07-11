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
| Gradient 128x128 | 1 | 4.896 ms | 866 | 86,186 | 20 |
| Gradient 128x128 | 50 | 4.448 ms | 2,768 | 90,730 | 20 |
| Gradient 128x128 | 75 | 5.053 ms | 3,506 | 93,802 | 20 |
| Gradient 128x128 | 90 | 11.508 ms | 5,284 | 152,746 | 24 |
| Gradient 128x128 | 100 | 11.936 ms | 8,306 | 164,522 | 21 |
| UI 256x256 | 75 | 17.542 ms | 2,906 | 306,096 | 21 |
| Flat 128x128 | 75 | 4.046 ms | 84 | 89,274 | 19 |
| Palette 256x256 | 75 | 26.382 ms | 39,042 | 394,496 | 23 |
| Alpha 128x128 | 75 | 6.195 ms | 5,582 | 126,218 | 33 |
| Photo-like 512x512 | 75 | 115.525 ms | 142,078 | 1,467,968 | 22 |

The photo-like fixture is deterministic synthetic content and is not a
substitute for a natural-photo corpus.

## Lossless

| Fixture | Time | encoded_B | B/op | allocs/op |
| --- | ---: | ---: | ---: | ---: |
| Gradient 128x128 | 326.135 ms | 58 | 18,453,264 | 10,957 |
| UI 256x256 | 650.467 ms | 1,284 | 58,717,701 | 19,167 |
| Flat 128x128 | 200.477 ms | 32 | 14,056,565 | 3,130 |
| Palette 256x256 | 474.438 ms | 834 | 54,151,536 | 21,041 |
| Alpha 128x128 | 327.292 ms | 322 | 23,889,189 | 18,117 |
| Photo-like 512x512 | 1,628.209 ms | 2,916 | 160,735,810 | 27,370 |

## Interpretation

- Q75 lossy encodes for the generated fixtures through 256x256 remain below
  30 ms on this machine; the photo-like 512x512 fixture takes about 115 ms
- High-quality lossy encoding costs more because it enables a broader source
  and mode search
- Lossy reconstruction uses a two-macroblock-row ring, reducing the estimated
  1024x1024 reconstruction workspace from about 1.5 MiB to 48 KiB without
  changing the encoded stream
- Lossless performance varies substantially with image structure. The
  photo-like 512x512 fixture takes about 1.63 seconds and reports about
  160.7 MB of cumulative allocation per encode
- `B/op` is cumulative heap allocation, not peak live memory. Candidate
  rematerialization can increase `B/op` while encode-scoped workspaces keep
  simultaneously retained search state bounded
- Lossless screening keeps transform descriptors and cost summaries, then
  performs exact match, cache, Huffman, and spatial-entropy optimization only
  for a bounded finalist set
- Default buffered search uses a 96 MiB estimated workspace gate and at most
  two finalist workers. Inputs outside that gate fall back to the streaming
  encoder; `BestCompression` raises the search gate to 192 MiB and permits up
  to four workers
- The broader transform, optimal-LZ77, color-cache, and entropy search favors
  encoded size over latency; use `ModeFast` or `ModeLowMemory` when latency or
  retained state is more important
- Current lossy latency does not justify architecture-specific assembly in the
  project scope, where encoded size and decoded quality take priority
- Before adding SIMD or assembly, profile-guided work should identify a stable
  transform or pixel-processing hotspot and demonstrate a meaningful end-to-end
  benefit on both amd64 and arm64
