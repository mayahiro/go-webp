package webp

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

func TestEncodeLossyConfigMatchesPublicModes(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 24))
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(7 * x), G: uint8(9 * y), B: uint8(3 * (x + y)), A: 255})
		}
	}
	for _, mode := range []Mode{ModeDefault, ModeBestCompression} {
		var public bytes.Buffer
		if err := Encode(&public, img, &Options{Compression: CompressionLossy, Quality: 75, Mode: mode}); err != nil {
			t.Fatalf("Encode mode %d: %v", mode, err)
		}
		var configured bytes.Buffer
		if err := encodeLossyConfig(
			&configured,
			newEncoderSource(img),
			vp8LossyConfigForModeQuality(mode, 75),
			lossyAlphaConfigForMode(mode),
		); err != nil {
			t.Fatalf("encodeLossyConfig mode %d: %v", mode, err)
		}
		if !bytes.Equal(configured.Bytes(), public.Bytes()) {
			t.Fatalf("configured mode %d output differed from public Encode", mode)
		}
	}
}
