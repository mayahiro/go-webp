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

Balanced lossless profiles can stop transform search when color indexing is
clearly better on a low-color image. `ModeBestCompression` retains a broader
bounded transform search, while `ModeAuto` selects the fast lossless profile only
for very small indexed payloads.

## Lossless Encoding

The lossless encoder can select VP8L predictor, color, color-indexing,
subtract-green, and palette transforms when their estimated complete bit cost
is lower. Its bounded search includes:

- Single-symbol Huffman trees for constant channels
- Full 256-symbol channel histograms and normal Huffman trees
- A bounded packed color-index stream for small low-color inputs
- A multi-candidate LZ77 match finder with one-step lazy matching
- Cost-based optimal parsing for indexed and promising general-image streams
- Dynamically selected 1- to 11-bit color caches
- Selected combinations of spatial prediction, LZ77, and color cache coding
  for literal and transformed residual streams
- Tile-adaptive predictor modes and cross-color coefficients
- Entropy-clustered meta-prefix histograms with up to 32 coding groups
- A two-stage planner that shortlists candidates cheaply before comparing the
  complete emitted cost of optimal LZ77, color-cache, and histogram variants

The encoder does not use unbounded hash chains. This keeps work and memory
bounded, but some inputs can remain larger than output from encoders that use
broader searches.

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

- Lossless `ModeBestCompression` can use a converted pixel plane up to 32 MiB
  for safe parallel reads from custom image implementations
- Buffered lossy profiles can retain quantized residuals up to an estimated
  32 MiB and reuse them for statistics and final coding
- Lossy reconstructed pixels use a two-macroblock-row ring rather than a
  full-frame reconstruction plane
- `ModeBestCompression` can analyze independent lossless candidates with up
  to four workers
- Standard image types can run lossy frame planning and alpha analysis with
  two workers
- `ModeLowMemory` avoids the full-frame source plane, VP8 residual buffer,
  VP8L token stream, meta-prefix plan, and color-cache plan

Inputs above a buffer limit fall back to a bounded repeated-pass or sequential
path. Standard image types use direct readers where possible. Small images,
custom image implementations, and single-threaded runtimes use sequential
lossy frame and alpha analysis.

## Limits

- Lossless dimensions must be from 1 to 16384 pixels on each axis
- Lossy dimensions must be from 1 to 16383 pixels on each axis
- Only static images are encoded
- Image metadata is not preserved by the `image.Image` API
- Lossy alpha does not use general hash-chain LZ77 search or block-adaptive
  alpha entropy coding
- Lossy loop-filter settings remain conservative and are not selected by an
  image-specific perceptual optimization pass
