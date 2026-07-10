package benchmarkmetric

import (
	"image"
	"image/color"
	"math"
	"testing"
)

func TestMeasureReportsExactImages(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	for index := range img.Pix {
		img.Pix[index] = uint8(index * 7)
	}
	metrics, err := Measure(img, img)
	if err != nil {
		t.Fatal(err)
	}
	if !metrics.RGBExact || !metrics.AlphaExact || metrics.RGBPSNRDB != nil || metrics.YSSIM != 1 || metrics.YSSIMDB != nil {
		t.Fatalf("exact metrics = %#v", metrics)
	}
}

func TestMeasureReportsChangedImages(t *testing.T) {
	source := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	decoded := image.NewNRGBA(source.Bounds())
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: 100, G: 100, B: 100, A: 255})
			decoded.SetNRGBA(x, y, color.NRGBA{R: 110, G: 110, B: 110, A: 254})
		}
	}
	metrics, err := Measure(source, decoded)
	if err != nil {
		t.Fatal(err)
	}
	wantSSIM := float64(2*100*110+20) / float64(100*100+110*110+20)
	if metrics.RGBExact || metrics.AlphaExact || math.Abs(metrics.YSSIM-wantSSIM) > 1e-12 {
		t.Fatalf("changed metrics = %#v, want Y SSIM %.15f", metrics, wantSSIM)
	}
}

func TestMeasureRejectsDimensionMismatch(t *testing.T) {
	if _, err := Measure(image.NewNRGBA(image.Rect(0, 0, 2, 1)), image.NewNRGBA(image.Rect(0, 0, 1, 1))); err == nil {
		t.Fatal("Measure accepted mismatched dimensions")
	}
}

func TestMeasurePlaneSSIMRejectsInvalidDimensions(t *testing.T) {
	if _, err := measurePlaneSSIM([]byte{1}, []byte{1}, 2, 1); err == nil {
		t.Fatal("measurePlaneSSIM accepted invalid dimensions")
	}
}
