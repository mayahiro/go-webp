package webp_test

import (
	"bytes"
	"context"
	"fmt"
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

func ExampleEncodeContext() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	img := image.NewNRGBA(image.Rect(0, 0, 16, 16))
	var output bytes.Buffer
	if err := webp.EncodeContext(ctx, &output, img, nil); err != nil {
		panic(err)
	}
	fmt.Println(output.Len() > 0)
	// Output: true
}
