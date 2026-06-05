# Benchmarks

This document records reproducible local benchmark results for the current
encoder implementation. Treat the numbers as development references, not as
portable performance guarantees.

## Environment

- Date: 2026-06-05
- OS: darwin
- Architecture: arm64
- CPU: Apple M1 Max
- Go command: local default Go toolchain unless otherwise noted

## Commands

```sh
go test ./... -run '^$' -bench 'BenchmarkEncodeLossy(Alpha128|AlphaBands512|AlphaNeighborhood512|Gradient1024|YCbCr512|Paletted512)$' -benchmem -count=3
```

```sh
go test . -run '^$' -bench 'BenchmarkEncodeLossyGradient1024$' -benchmem -benchtime=1x -cpuprofile go-webp-lossy-final-gradient1024.cpu -memprofile go-webp-lossy-final-gradient1024.mem
go tool pprof -top -nodecount=20 go-webp-lossy-final-gradient1024.cpu
go tool pprof -top -nodecount=20 -alloc_space go-webp-lossy-final-gradient1024.mem
```

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
