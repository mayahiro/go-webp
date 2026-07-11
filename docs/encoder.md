# Encoder Guide

[日本語](encoder_ja.md)

This guide describes go-webp's public encoding API, compression families,
search profiles, resource behavior, and current limits.

## Scope

go-webp encodes static WebP images from Go's standard `image.Image`
interface. It does not decode WebP, provide a command-line tool, or encode
animations. WebP decoding is available from
[`golang.org/x/image/webp`](https://pkg.go.dev/golang.org/x/image/webp).

## Public API

```go
func Encode(w io.Writer, m image.Image, o *Options) error
```

`Encode` writes the encoded image to `w`. A nil `Options` value and the zero
value both select lossless WebP.

```go
type Options struct {
	Compression Compression
	Quality     int
	Mode        Mode
}
```

An `Encoder` value is also available for code that follows the style of
`image/png.Encoder`:

```go
type Encoder struct {
	Options *Options
}

func (enc *Encoder) Encode(w io.Writer, m image.Image) error
```

## Compression Families

| Family | Selection | Behavior |
| --- | --- | --- |
| Lossless | Default or `CompressionLossless` | Writes VP8L and preserves every pixel exactly |
| Lossy | `CompressionLossy` | Writes a VP8 key frame with quality from 1 to 100 |
| Near-lossless | `ModeNearLossless` | Writes VP8L after bounded, edge-aware RGB quantization and preserves alpha |
| Explicit lossy | `ModeLossyQuality` | Writes VP8 lossy output regardless of `Compression` |

For lossy encoding, values of `Quality` less than or equal to zero use the
package default and values above 100 are clamped to 100. Ordinary lossless
encoding ignores `Quality`.

Near-lossless quality bounds the maximum RGB channel error as follows:

| Quality | Maximum RGB channel error |
| ---: | ---: |
| 100 or omitted | 0 |
| 80-99 | 1 |
| 60-79 | 2 |
| 40-59 | 4 |
| 20-39 | 8 |
| 1-19 | 16 |

Near-lossless inputs with both dimensions below 64 pixels, or with height
below 3 pixels, are left unchanged.

## Search Profiles

| Mode | Purpose |
| --- | --- |
| `ModeDefault` | Uses the behavior selected by `Compression` and `Quality` |
| `ModeFast` | Reduces search work and retained state |
| `ModeBalanced` | Uses the default size, quality, speed, and memory balance |
| `ModeBestCompression` | Evaluates additional bounded compression candidates |
| `ModeLowMemory` | Avoids full-frame source, residual, token, and cache buffers |
| `ModeNearLossless` | Selects VP8L near-lossless encoding |
| `ModeLossyQuality` | Selects VP8 lossy encoding |
| `ModeAuto` | Chooses a conservative internal profile from image features |

No profile guarantees the smallest or fastest output for every image.
`ModeFast` and `ModeLowMemory` can produce larger output because they
intentionally reduce search work.

Balanced lossless profiles use the default bounded transform, match, and
entropy budgets. `ModeBestCompression` broadens those budgets, while
`ModeAuto` selects the fast lossless profile only for very small verified
indexed payloads.

## Lossless Encoding

The lossless encoder compares complete VP8L plans rather than selecting each
feature independently. Buffered profiles use a two-stage bounded search:

- A transform graph covering direct, subtract-green, predictor, cross-color,
  palette, and selected combined transforms
- Tile-adaptive predictor modes and, in `ModeBestCompression`, optional
  block-adaptive cross-color coefficients
- Family-aware screening that combines exact literal bits with sampled local
  and distant match potential
- A compact reverse-built match graph with bounded hash chains and propagated
  run and previous-row matches
- Cost-based dynamic-programming parsing across literals, backward references,
  and 1- to 11-bit color-cache references
- Sparse spatial histograms and entropy clustering with up to 16 coding groups
- Exact size comparison using the same bit-writing logic used for emission
- Encode-scoped transform, match, Huffman, cache, entropy, and parser
  workspaces reused across finalists

`ModeFast` and `ModeLowMemory` use a row-streaming path with inexpensive
transforms and greedy matches. Buffered modes also fall back to streaming when
the source or estimated search workspace exceeds its configured limit.

`ModeBestCompression` retains the complete default plan as an incumbent and
selects its expanded-search result only when the exact payload is smaller.
It therefore does not produce a larger lossless payload than `ModeDefault` for
the same input.

The matcher does not use unbounded hash chains, and every profile limits
candidate counts, edges, parse iterations, entropy groups, workers, and
workspace estimates. Some inputs can therefore remain larger than output from
encoders with broader searches.

## Lossy Encoding

The lossy encoder writes an intra-only VP8 key frame with 4:2:0 chroma. Its
bounded analysis includes:

- Adaptive chroma downsampling and optional sharp chroma conversion
- Intra16x16, chroma, and optional luma4x4 prediction modes
- Up to four activity-adaptive quantizer segments
- Quality-dependent quantization bias and separate luma and chroma
  rate-distortion weights
- A bounded spectral texture term at medium qualities
- Joint selection of macroblock skip signaling and residual token
  probabilities when residual buffering is available
- A normal VP8 loop filter with quality-scaled settings and a luma4x4 mode
  delta

`ModeFast` and `ModeLowMemory` omit luma4x4 mode search. `ModeFast` also omits
macroblock skip and token-probability update search. `ModeBestCompression`
adds a second rate-distortion pass, trellis quantization, and sharp chroma
search.

## Alpha

Lossless and near-lossless encoding preserve alpha exactly. Lossy images with
alpha are written as extended WebP with an `ALPH` chunk. The encoder compares
compressed and raw alpha and writes the smaller representation.

Compressed lossy alpha uses a global filter, frequency-coded residuals, and
bounded backward references for repeated runs and previous-row spatial
matches. `ModeFast` limits search to unfiltered alpha and repeated runs.
`ModeLowMemory` retains filter search but omits previous-row candidates, while
`ModeBestCompression` applies bounded optimal parsing to its run and
previous-row candidates. Fully opaque standard image types skip alpha
candidate analysis; custom image implementations use the general analysis
path.

## Input and Resource Behavior

`image.NRGBA`, `image.RGBA`, `image.Gray`, `image.YCbCr`, and
`image.Paletted` use dedicated read paths. Other image implementations are
read through conversion equivalent to `color.NRGBAModel`.

Encoding can scan the input more than once. Important resource bounds include:

- Buffered lossless profiles use a packed source plane capped at 32 MiB
- Default buffered lossless search has a 96 MiB estimated workspace gate and
  up to two finalist workers
- `ModeBestCompression` has a 192 MiB estimated workspace gate and up to four
  finalist workers
- Inputs outside a buffered lossless gate fall back to row-streaming encoding
- Lossless screening retains rematerializable transform descriptors; exact
  finalist workspaces are encode-scoped and reused sequentially or by a
  bounded worker pool
- Buffered lossy profiles can retain quantized residuals up to an estimated
  32 MiB and reuse them for statistics and final coding
- Lossy reconstructed pixels use a two-macroblock-row ring rather than a
  full-frame reconstruction plane
- Standard image types can run lossy frame planning and alpha analysis with
  two workers
- `ModeLowMemory` avoids the full-frame source plane, VP8 residual buffer,
  VP8L token stream, meta-prefix plan, and color-cache plan

`B/op` from Go benchmarks measures cumulative allocation, not peak live memory.
Candidate rematerialization can make cumulative allocation exceed the search
workspace estimate while simultaneously retained state remains bounded.
Standard image types use direct readers where possible.

Encoded bytes are not a stable serialization contract. Encoder versions may
select different valid VP8L or VP8 plans while preserving the documented pixel
behavior and public API.

## Limits

- Lossless dimensions must be from 1 to 16384 pixels on each axis
- Lossy dimensions must be from 1 to 16383 pixels on each axis
- Only static images are encoded
- Image metadata is not preserved by the `image.Image` API
- Lossy alpha does not use general hash-chain LZ77 search or block-adaptive
  alpha entropy coding
- Lossy loop-filter settings remain conservative and are not selected by an
  image-specific perceptual optimization pass
