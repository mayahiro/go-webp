# go-webp

go-webp is a pure Go WebP encoder for the standard `image.Image`
interface.

The encoder writes VP8L lossless WebP images by default and can also write
VP8-based lossy WebP images. It is designed to fit the shape of Go's standard
image packages: callers pass an `io.Writer`, an `image.Image`, and optional
encoder options.

## Installation

```sh
go get github.com/mayahiro/go-webp
```

## Usage

```go
package main

import (
	"image"
	"image/color"
	"os"

	"github.com/mayahiro/go-webp"
)

func main() {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 2))
	img.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 255})
	img.SetNRGBA(1, 0, color.NRGBA{G: 255, A: 255})
	img.SetNRGBA(0, 1, color.NRGBA{B: 255, A: 255})
	img.SetNRGBA(1, 1, color.NRGBA{R: 255, G: 255, B: 255, A: 255})

	f, err := os.Create("out.webp")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	if err := webp.Encode(f, img, nil); err != nil {
		panic(err)
	}
}
```

## API

```go
func Encode(w io.Writer, m image.Image, o *Options) error
```

`Encode` writes a lossless WebP image to `w`. A nil `Options` value uses the
default lossless settings.

Use `CompressionLossy` to write a lossy WebP image:

```go
err := webp.Encode(w, img, &webp.Options{
	Compression: webp.CompressionLossy,
	Quality:     80,
})
```

`Quality` controls lossy quality from 1 to 100. Values less than or equal to
zero use the default, values greater than 100 are clamped to 100, and the field
is ignored for lossless encoding.

`Mode` can tune the encoder search profile or select an explicit output family:

```go
err := webp.Encode(w, img, &webp.Options{
	Mode:    webp.ModeNearLossless,
	Quality: 75,
})
```

`ModeDefault` preserves the behavior selected by `Compression` and `Quality`.
`ModeFast`, `ModeBalanced`, `ModeBestCompression`, `ModeLowMemory`, and
`ModeAuto` tune the selected compression mode. `ModeNearLossless` writes VP8L
with alpha preserved and edge-aware RGB quantization controlled by `Quality`;
quality 100, or an omitted quality, is equivalent to lossless. Qualities 80-99,
60-79, 40-59, 20-39, and 1-19 limit the maximum RGB channel error to 1, 2, 4,
8, and 16 respectively. Images with both dimensions below 64 pixels, or with
height below 3 pixels, are kept unchanged, matching cwebp's small-image
near-lossless behavior.
`ModeLossyQuality` writes VP8 lossy output and uses `Quality`, regardless of
`Compression`.

```go
type Encoder struct {
	Options *Options
}

func (enc *Encoder) Encode(w io.Writer, m image.Image) error
```

`Encoder` mirrors the style of encoders such as `image/png.Encoder` and keeps
room for future options.

## Performance Notes

- The encoder is pure Go and does not use cgo.
- See [BENCHMARKS.md](BENCHMARKS.md) for current local benchmark references.
- It scans the source image multiple times. Lossless BestCompression may use a
  converted pixel plane up to 32 MiB for safe parallel reads of custom image
  types; other lossless profiles keep direct readers.
- Small low-color inputs can keep a bounded packed color-index stream so
  repeated LZ77 evaluation does not repeat source color lookup.
- Constant channels are encoded with single-symbol Huffman trees.
- The lossless encoder can use VP8L predictor, color, color-indexing, and
  subtract-green transforms when they are estimated to reduce output size.
  `ModeBestCompression` also tries a block-adaptive predictor candidate. The
  encoder can use simple VP8L LZ77 backwards references with a bounded
  multi-candidate hash match finder and one-step lazy matching. Indexed streams
  can use bounded cost-based optimal parsing, and indexed data can be combined
  with a spatial predictor when its complete bit cost is lower. It can use a
  limited VP8L color cache path for literal streams when a sample and bit-cost
  estimate indicate that it should help, including bounded LZ77 plus color
  cache paths for untransformed streams and selected predictor or color
  transform residual streams. `ModeBestCompression` also compares one bounded,
  token-driven meta-prefix histogram grouping candidate for coarse regional
  entropy differences. It does not use unbounded hash chains, so lossless
  output can be larger than highly optimized WebP encoders.
- `ModeFast` and `ModeLowMemory` intentionally reduce lossless search work and
  may produce larger VP8L files. Balanced profiles can stop transform search
  early when color indexing is clearly better on a low-color image, while
  `ModeBestCompression` retains exhaustive transform search. `ModeAuto` uses
  conservative image-feature checks, only chooses the fast lossless profile
  for very small indexed payloads, and does not guarantee the smallest or
  fastest output for every image.
- For lossy images with alpha, `ModeFast` limits `ALPH` search to unfiltered
  alpha and repeated-run coding, while `ModeLowMemory` keeps filter search but
  skips previous-row spatial reference candidates. `ModeBestCompression`
  additionally applies bounded optimal parsing to the run and previous-row
  match candidates.
- `ModeBestCompression` can analyze independent lossless transform candidates
  with up to four workers. Standard image types are read directly; custom
  image types use a pixel plane limited to 32 MiB, and larger inputs fall back
  to sequential analysis.
- Lossy VP8 frame planning and `ALPH` analysis can run with two workers for
  standard image types. `ModeFast`, `ModeLowMemory`, small images, custom image
  types, and single-threaded runtimes use the sequential path.
- Lossy encoding uses the standard image types' opacity checks to skip `ALPH`
  candidate analysis when the input is fully opaque. Custom image types retain
  the general pixel-analysis path.
- For lossy VP8 output, `ModeFast` keeps the requested quality mapping but
  disables luma4x4 mode search, macroblock skip signaling, and token
  probability update search. `ModeLowMemory` also omits luma4x4 mode search.
  `ModeBestCompression` enables a second rate-distortion pass, trellis
  quantization, and bounded sharp-chroma search.
- Lossy profiles that use skip or token probability analysis can retain the
  selected quantized residuals and reuse them for statistics and final coding,
  instead of repeating the macroblock DCT and reconstruction passes. This
  buffer is limited to an estimated 32 MiB. `ModeFast`, `ModeLowMemory`, and
  images above the limit use the repeated-pass path without this buffer.
- VP8 mode passes reuse reconstruction, top-row context, skip-map, and residual
  workspaces within one encode. `ModeLowMemory` does not retain a source plane,
  VP8 residual buffer, VP8L token stream, meta-prefix plan, or color-cache plan.
- Lossy encoding uses a low-complexity VP8 key frame encoder with 4:2:0 chroma
  subsampling, adaptive chroma downsampling, selected intra16x16 and chroma
  prediction modes, optional luma4x4 modes, and quantized DC and AC
  coefficients. The default lossy profiles search luma4x4 modes and can use up
  to four activity-adaptive quantizer segments. They use quality-dependent
  quantization bias and rate-distortion weights, including a bounded spectral
  texture term at medium qualities and bounded sharp-chroma search at high
  qualities. The encoder writes
  residual token probability updates when they are estimated to reduce the
  frame size and enables the normal VP8 loop filter with quality-scaled
  sharpness and a mode delta for luma4x4 macroblocks.
- Lossy `Quality` currently uses a non-linear mapping to the VP8 base quantizer
  and quality-dependent Y2/UV quantization and loop filter settings. The
  encoder uses activity-based segmentation and rate-distortion mode decisions.
- Lossy images with alpha are written as extended WebP files with an `ALPH`
  chunk. The encoder uses compressed alpha when it is smaller and falls back to
  raw alpha otherwise. Compressed alpha uses frequency-coded residuals and
  backward references for repeated residual runs and previous-row residual
  matches across the VP8L spatial-distance neighborhood.

## Limitations

- Encoding only. Decoding is not implemented.
- Lossless image dimensions must be between 1 and 16384 pixels on each axis.
- Lossy image dimensions must be between 1 and 16383 pixels on each axis.
- Standard image types such as `image.NRGBA`, `image.RGBA`, `image.Gray`,
  `image.YCbCr`, and `image.Paletted` use dedicated read paths. Other image
  types are read through `color.NRGBAModel`-equivalent conversion before
  encoding.
- Lossy alpha compression is intentionally simple and currently uses one global
  `ALPH` filter, frequency-coded residuals, and bounded backward-reference
  parsing for repeated residual runs and previous-row spatial matches. It does
  not yet perform general hash-chain LZ77 search or block-adaptive alpha entropy
  coding.
- Lossy loop filter settings are intentionally conservative and are not yet
  tuned with image-specific perceptual metrics.

## Supported Environments

- Go 1.25.0 or later.

## Verification

```sh
go test ./...
go vet ./...
go tool goimports -l .
```

Optional external decoder check:

```sh
go run ./scripts/verify_lossless_external
```

The external decoder check verifies lossless fixtures with exact pixel
comparison and lossy fixtures with bounded RGB error. It prefers `dwebp` when
it is available, otherwise it uses a temporary `golang.org/x/image/webp` decoder
through `go run`, and falls back to macOS `sips` only when the other decoders
are unavailable.

For local lossless, profile, or near-lossless comparisons against libwebp:

```sh
go run ./scripts/compare_lossless_libwebp -runs 3 -mode default -method 4
go run ./scripts/compare_lossless_libwebp -runs 3 -mode best -method 6
go run ./scripts/compare_lossless_libwebp -runs 3 -mode near-lossless -quality 75 -method 4
```

The table reports decoded RGB error and alpha equality. Ordinary lossless
profiles require exact pixels.

For a local lossy rate-distortion comparison against libwebp:

```sh
go run ./scripts/compare_lossy_libwebp -runs 3 -go-mode default -json report.json
```

The lossy comparison requires `cwebp` and `dwebp`. Its JSON report contains
quality sweeps, decoded RGB/YUV and alpha metrics, weighted 7x7 Y SSIM,
encode timing, VP8 partition sizes, and the nearest sampled cwebp points by
encoded size and Y SSIM dB. A private local corpus can be selected with
`-corpus` and `-split`; source names and paths are omitted from the report. The
go-webp timing covers the in-process `Encode` call, while the cwebp timing also
includes process startup, PNG decoding, and output writing.

## License

MIT
