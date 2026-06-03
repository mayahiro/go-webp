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
- It scans the source image twice and does not keep a full converted image in
  memory for lossless encoding.
- Constant channels are encoded with single-symbol Huffman trees.
- The current encoder does not use VP8L transforms, color cache, or LZ77
  backwards references, so output can be larger than highly optimized WebP
  encoders.
- Lossy encoding uses a low-complexity VP8 key frame encoder with 4:2:0 chroma
  subsampling, selected intra16x16 and chroma prediction modes, and quantized
  DC and AC coefficients. It enables the simple VP8 loop filter with a
  quality-scaled filter level.
- Lossy `Quality` currently controls the VP8 base quantizer while the encoder
  uses a simple mode decision heuristic.
- Lossy images with alpha are written as extended WebP files with an `ALPH`
  chunk. The encoder uses compressed alpha when it is smaller and falls back to
  raw alpha otherwise.

## Limitations

- Encoding only. Decoding is not implemented.
- Lossless image dimensions must be between 1 and 16384 pixels on each axis.
- Lossy image dimensions must be between 1 and 16383 pixels on each axis.
- Non-`image.NRGBA` images are converted through `color.NRGBAModel` before
  encoding.
- Lossy alpha compression is intentionally simple and currently uses
  frequency-coded alpha residuals without LZ77 references.
- Lossy 4x4 luma prediction mode selection and normal loop filtering are not
  implemented yet, so detailed images can still be blockier or larger than
  optimized VP8/WebP encoders.

## Supported Environments

- Go 1.25.0 or later.

## Verification

```sh
go test ./...
go vet ./...
go tool goimports -w .
```

## License

MIT
