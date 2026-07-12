package webp

import (
	"image"
	"image/color"
	"testing"
)

func TestYCbCrToVP8LimitedRange(t *testing.T) {
	lumaAnchors := map[uint8]uint8{0: 16, 128: 126, 255: 235}
	chromaAnchors := map[uint8]uint8{0: 16, 127: 127, 128: 128, 255: 240}
	for input, want := range lumaAnchors {
		if got := ycbcrToVP8Luma(input); got != want {
			t.Fatalf("luma %d = %d, want %d", input, got, want)
		}
	}
	for input, want := range chromaAnchors {
		gotCb, gotCr := ycbcrToVP8Chroma(input, input)
		if gotCb != want || gotCr != want {
			t.Fatalf("chroma %d = (%d,%d), want (%d,%d)", input, gotCb, gotCr, want, want)
		}
	}
	for input := 1; input < 256; input++ {
		if ycbcrToVP8LumaTable[input] < ycbcrToVP8LumaTable[input-1] {
			t.Fatalf("luma table decreases at %d", input)
		}
		if ycbcrToVP8ChromaTable[input] < ycbcrToVP8ChromaTable[input-1] {
			t.Fatalf("chroma table decreases at %d", input)
		}
	}
}

func TestYCbCrToVP8DirectTransformTracksInGamutRGB(t *testing.T) {
	maxLumaDelta := 0
	maxChromaDelta := 0
	for r := 0; r < 256; r += 17 {
		for g := 0; g < 256; g += 17 {
			for b := 0; b < 256; b += 17 {
				y, cb, cr := color.RGBToYCbCr(uint8(r), uint8(g), uint8(b))
				gotY := ycbcrToVP8Luma(y)
				gotCb, gotCr := ycbcrToVP8Chroma(cb, cr)
				wantY := rgbToLumaValue(uint8(r), uint8(g), uint8(b))
				wantCb, wantCr := rgbToChromaValue(uint8(r), uint8(g), uint8(b))
				maxLumaDelta = max(maxLumaDelta, absInt(int(gotY)-int(wantY)))
				maxChromaDelta = max(maxChromaDelta, absInt(int(gotCb)-int(wantCb)), absInt(int(gotCr)-int(wantCr)))
			}
		}
	}
	if maxLumaDelta > 1 || maxChromaDelta > 1 {
		t.Fatalf("maximum direct transform delta = luma:%d chroma:%d, want <= 1", maxLumaDelta, maxChromaDelta)
	}
}

func TestYCbCrReadersForAllSubsampleRatios(t *testing.T) {
	bounds := image.Rect(-3, 5, 14, 18)
	for _, ratio := range testYCbCrSubsampleRatios() {
		t.Run(ratio.String(), func(t *testing.T) {
			img := newTestYCbCr(bounds, ratio)
			readLuma := lumaReaderFor(img)
			readChroma := chromaReaderFor(img)
			for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
				for x := bounds.Min.X; x < bounds.Max.X; x++ {
					yy := img.Y[img.YOffset(x, y)]
					ci := img.COffset(x, y)
					if got, want := readLuma(x, y), ycbcrToVP8LumaTable[yy]; got != want {
						t.Fatalf("luma (%d,%d) = %d, want %d", x, y, got, want)
					}
					gotCb, gotCr := readChroma(x, y)
					wantCb := ycbcrToVP8ChromaTable[img.Cb[ci]]
					wantCr := ycbcrToVP8ChromaTable[img.Cr[ci]]
					if gotCb != wantCb || gotCr != wantCr {
						t.Fatalf("chroma (%d,%d) = (%d,%d), want (%d,%d)", x, y, gotCb, gotCr, wantCb, wantCr)
					}
				}
			}
		})
	}
}

func TestYCbCrReadersClampOddImageEdges(t *testing.T) {
	bounds := image.Rect(-3, 5, 14, 18)
	img := newTestYCbCr(bounds, image.YCbCrSubsampleRatio420)
	readLuma := lumaReaderFor(img)
	readChroma := chromaReaderFor(img)
	for _, tc := range []struct {
		x     int
		y     int
		wantX int
		wantY int
	}{
		{x: -1, y: -1, wantX: 0, wantY: 0},
		{x: bounds.Dx(), y: 0, wantX: bounds.Dx() - 1, wantY: 0},
		{x: 0, y: bounds.Dy(), wantX: 0, wantY: bounds.Dy() - 1},
		{x: bounds.Dx() + 3, y: bounds.Dy() + 7, wantX: bounds.Dx() - 1, wantY: bounds.Dy() - 1},
	} {
		if got, want := sampleLuma(readLuma, bounds, tc.x, tc.y), readLuma(bounds.Min.X+tc.wantX, bounds.Min.Y+tc.wantY); got != want {
			t.Fatalf("clamped luma (%d,%d) = %d, want %d", tc.x, tc.y, got, want)
		}
		gotCb, gotCr := sampleChroma(readChroma, bounds, tc.x, tc.y)
		wantCb, wantCr := readChroma(bounds.Min.X+tc.wantX, bounds.Min.Y+tc.wantY)
		if gotCb != wantCb || gotCr != wantCr {
			t.Fatalf("clamped chroma (%d,%d) = (%d,%d), want (%d,%d)", tc.x, tc.y, gotCb, gotCr, wantCb, wantCr)
		}
	}
}

func testYCbCrSubsampleRatios() []image.YCbCrSubsampleRatio {
	return []image.YCbCrSubsampleRatio{
		image.YCbCrSubsampleRatio444,
		image.YCbCrSubsampleRatio422,
		image.YCbCrSubsampleRatio420,
		image.YCbCrSubsampleRatio440,
		image.YCbCrSubsampleRatio411,
		image.YCbCrSubsampleRatio410,
	}
}

func newTestYCbCr(bounds image.Rectangle, ratio image.YCbCrSubsampleRatio) *image.YCbCr {
	img := image.NewYCbCr(bounds, ratio)
	for i := range img.Y {
		img.Y[i] = uint8(i*37 + 11)
	}
	for i := range img.Cb {
		img.Cb[i] = uint8(i*53 + 3)
		img.Cr[i] = uint8(i*29 + 197)
	}
	return img
}

func legacyVP8SourceForYCbCr(img *image.YCbCr, materialize bool) vp8Source {
	bounds := img.Bounds()
	result := vp8Source{
		bounds: bounds,
		width:  bounds.Dx(),
		height: bounds.Dy(),
	}
	readLuma, readChroma := legacyYCbCrReaders(img)
	if !materialize {
		result.readLuma = readLuma
		result.readChroma = readChroma
		return result
	}
	total := result.width * result.height
	data := make([]uint8, total*3)
	plane := vp8SourcePlane{
		data:   data,
		width:  result.width,
		minX:   bounds.Min.X,
		minY:   bounds.Min.Y,
		cbBase: total,
		crBase: total * 2,
	}
	readPixel := pixelReaderFor(img)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		row := (y - bounds.Min.Y) * result.width
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			index := row + x - bounds.Min.X
			pixel := readPixel(x, y)
			data[index] = rgbToLumaValue(pixel.R, pixel.G, pixel.B)
			data[plane.cbBase+index], data[plane.crBase+index] = rgbToChromaValue(pixel.R, pixel.G, pixel.B)
		}
	}
	result.plane = plane
	result.readLuma = plane.luma
	result.readChroma = plane.chroma
	return result
}

func legacyYCbCrReaders(img *image.YCbCr) (lumaReader, chromaReader) {
	readLuma := func(x int, y int) uint8 {
		yy := img.Y[img.YOffset(x, y)]
		ci := img.COffset(x, y)
		r, g, b := color.YCbCrToRGB(yy, img.Cb[ci], img.Cr[ci])
		return rgbToLumaValue(r, g, b)
	}
	readChroma := func(x int, y int) (uint8, uint8) {
		yy := img.Y[img.YOffset(x, y)]
		ci := img.COffset(x, y)
		r, g, b := color.YCbCrToRGB(yy, img.Cb[ci], img.Cr[ci])
		return rgbToChromaValue(r, g, b)
	}
	return readLuma, readChroma
}
