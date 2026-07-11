package webp

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

func TestVP8LBlockColorTransformRoundTrip(t *testing.T) {
	const width, height = 64, 32
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			green := uint8((x*7 + y*11) & 0xff)
			red := green
			blue := green * 2
			if x >= width/2 {
				red = 0 - green
				blue = 255 - green
			}
			img.SetNRGBA(x, y, color.NRGBA{R: red, G: green, B: blue, A: 255})
		}
	}
	source := newEncoderSource(img)
	pixels, alpha, err := newVP8LSource(source, source.pixels()).materialize(vp8lMaxSourceBytes)
	if err != nil {
		t.Fatal(err)
	}
	const sizeBits = 4
	elements, transformWidth, transformHeight := vp8lChooseBlockColorElements(pixels, width, height, sizeBits, 0)
	if vp8lAllColorElementsEqual(elements) {
		t.Fatal("block color search selected one global element")
	}
	transformed := vp8lApplyBlockColorTransform(pixels, width, height, sizeBits, elements, transformWidth)
	transform := vp8lBlockColorTransform(sizeBits, elements, transformWidth, transformHeight)
	plan := newVP8LPlan(width, height, alpha, []vp8lTransform{transform}, transformed, width, height, vp8lBudgetForMode(ModeDefault))
	var output bytes.Buffer
	if err := writeLosslessVP8L(&output, plan); err != nil {
		t.Fatal(err)
	}
	assertVP8LRoundTrip(t, output.Bytes(), img)
}
