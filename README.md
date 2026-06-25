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
with alpha preserved and RGB quantized according to `Quality`; quality 100, or
an omitted quality, is equivalent to lossless. `ModeLossyQuality` writes VP8
lossy output and uses `Quality`, regardless of `Compression`.

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
- It scans the source image multiple times and does not keep a full converted
  image in memory for lossless encoding.
- Constant channels are encoded with single-symbol Huffman trees.
- The lossless encoder can use VP8L predictor, color, color-indexing, and
  subtract-green transforms when they are estimated to reduce output size.
  `ModeBestCompression` also tries a block-adaptive predictor candidate. The
  encoder can use simple VP8L LZ77 backwards references with a bounded
  multi-candidate hash match finder and one-step lazy matching. It can use a
  limited VP8L color cache path for literal streams when a sample and bit-cost
  estimate indicate that it should help, including bounded LZ77 plus color
  cache paths for untransformed streams and selected predictor or color
  transform residual streams. It does not use unbounded hash chains, so
  lossless output can be larger than highly optimized WebP encoders.
- `ModeFast` and `ModeLowMemory` intentionally reduce lossless search work and
  may produce larger VP8L files. `ModeAuto` uses conservative image-feature
  checks, only chooses the fast lossless profile for very small indexed payloads,
  and does not guarantee the smallest or fastest output for every image.
- For lossy images with alpha, `ModeFast` limits `ALPH` search to unfiltered
  alpha and repeated-run coding, while `ModeLowMemory` keeps filter search but
  skips previous-row spatial reference candidates.
- For lossy VP8 output, `ModeFast` keeps the requested quality mapping but
  disables macroblock skip signaling and token probability update search.
  `ModeBestCompression` additionally enables luma4x4 mode search.
- Lossy encoding uses a low-complexity VP8 key frame encoder with 4:2:0 chroma
  subsampling, adaptive chroma downsampling, selected intra16x16 and chroma
  prediction modes, optional luma4x4 modes in `ModeBestCompression`, and
  quantized DC and AC coefficients. It writes
  residual token probability updates when they are estimated to reduce the
  frame size and enables the normal VP8 loop filter with quality-scaled
  sharpness and a mode delta for luma4x4 macroblocks.
- Lossy `Quality` currently uses a non-linear mapping to the VP8 base quantizer
  and quality-dependent Y2/UV quantization and loop filter settings. The
  encoder uses a simple rate-distortion mode decision heuristic.
- Lossy images with alpha are written as extended WebP files with an `ALPH`
  chunk. The encoder uses compressed alpha when it is smaller and falls back to
  raw alpha otherwise. Compressed alpha uses frequency-coded residuals and
  backward references for repeated residual runs, previous-row residual
  matches, and neighboring previous-row residual matches.

## Limitations

- Encoding only. Decoding is not implemented.
- Lossless image dimensions must be between 1 and 16384 pixels on each axis.
- Lossy image dimensions must be between 1 and 16383 pixels on each axis.
- Standard image types such as `image.NRGBA`, `image.RGBA`, `image.Gray`,
  `image.YCbCr`, and `image.Paletted` use dedicated read paths. Other image
  types are read through `color.NRGBAModel`-equivalent conversion before
  encoding.
- Lossy alpha compression is intentionally simple and currently uses one global
  `ALPH` filter, frequency-coded residuals, and limited backward references for
  repeated residual runs, previous-row residual matches, and neighboring
  previous-row residual matches. It does not yet perform general LZ77 match
  search or block-adaptive alpha entropy coding.
- Lossy loop filter settings are intentionally conservative and are not yet
  tuned with image-specific perceptual metrics.

## Supported Environments

- Go 1.25.0 or later.

## Verification

```sh
go test ./...
go vet ./...
go tool goimports -w .
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

For a local lossless comparison against libwebp:

```sh
go run ./scripts/compare_lossless_libwebp -runs 3
```

## License

MIT
