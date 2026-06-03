# go-webp

go-webp is a pure Go WebP encoder for the standard `image.Image`
interface.

The encoder writes VP8L lossless WebP images by default and can also write a
simple VP8 lossy WebP image. It is designed to fit the shape of Go's standard
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

Use `CompressionLossy` to write a simple lossy WebP image:

```go
err := webp.Encode(w, img, &webp.Options{
	Compression: webp.CompressionLossy,
})
```

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
  subsampling, DC prediction, and DC coefficients only.

## Limitations

- Encoding only. Decoding is not implemented.
- Lossless image dimensions must be between 1 and 16384 pixels on each axis.
- Lossy image dimensions must be between 1 and 16383 pixels on each axis.
- Non-`image.NRGBA` images are converted through `color.NRGBAModel` before
  encoding.
- Lossy encoding does not preserve alpha. Use lossless encoding when alpha must
  be retained.
- Lossy output is intentionally simple and can be blockier or larger than
  output from highly optimized VP8/WebP encoders.

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
