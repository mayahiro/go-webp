# go-webp

[日本語](README_ja.md)

go-webp is a quality-focused WebP encoding library for Go's standard
`image.Image` interface. It writes static VP8L lossless, VP8L near-lossless,
and VP8 lossy images through a small API shaped like Go's standard image
encoders.

## Features

- Pure Go implementation with no cgo, native library, or
  architecture-specific assembly
- Lossless, near-lossless, and true VP8 lossy encoding
- Alpha preservation for every compression family
- Compression profiles for fast, balanced, best-compression, automatic, and
  low-memory operation
- Direct support for common standard image types and a general `image.Image`
  fallback
- Go 1.25.0 or later

go-webp intentionally focuses on static image encoding. For decoding, use
[`golang.org/x/image/webp`](https://pkg.go.dev/golang.org/x/image/webp).

## Installation

```sh
go get github.com/mayahiro/go-webp
```

## Usage

The following example converts a JPEG image to lossy WebP at quality 80:

```go
package main

import (
	"image/jpeg"
	"os"

	webp "github.com/mayahiro/go-webp"
)

func main() {
	src, err := os.Open("input.jpg")
	if err != nil {
		panic(err)
	}
	defer src.Close()

	img, err := jpeg.Decode(src)
	if err != nil {
		panic(err)
	}

	dst, err := os.Create("output.webp")
	if err != nil {
		panic(err)
	}
	defer dst.Close()

	err = webp.Encode(dst, img, &webp.Options{
		Compression: webp.CompressionLossy,
		Quality:     80,
	})
	if err != nil {
		panic(err)
	}
}
```

Pass `nil` options to write lossless WebP:

```go
err := webp.Encode(dst, img, nil)
```

## Performance

Representative measurements on an Apple M1 Max, darwin/arm64, with Go 1.26.5:

| Encoding | Fixture | Time | Encoded size |
| --- | --- | ---: | ---: |
| Lossy Q75 | UI 256x256 | 17.542 ms | 2,906 bytes |
| Lossy Q75 | Photo-like 512x512 | 115.525 ms | 142,078 bytes |
| Lossless | Gradient 128x128 | 33.987 ms | 58 bytes |
| Lossless | Photo-like 512x512 | 1,728.762 ms | 18,918 bytes |

These are local development references, not portable guarantees. The fixtures
are deterministic synthetic images. See [Benchmarks](BENCHMARKS.md) for the
methodology and complete results.

## Documentation

- [Encoder guide](docs/encoder.md): API, compression families, profiles,
  alpha handling, limits, and resource behavior
- [Architecture](ARCHITECTURE.md): package boundaries and encoding pipelines
- [Benchmarks](BENCHMARKS.md): reproducible performance measurements
- [Development guide](docs/development.md): verification and local comparison
  commands

## License

MIT
