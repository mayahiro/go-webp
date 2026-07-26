package webp_test

import (
	"bytes"
	"image"

	webp "github.com/mayahiro/go-webp"
)

func ExampleEncode() {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))

	var lossless bytes.Buffer
	if err := webp.Encode(&lossless, img, &webp.Options{}); err != nil {
		panic(err)
	}

	var lossy bytes.Buffer
	if err := webp.Encode(&lossy, img, &webp.Options{
		Compression: webp.CompressionLossy,
		Quality:     80,
	}); err != nil {
		panic(err)
	}
}
