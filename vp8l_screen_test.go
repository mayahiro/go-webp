package webp

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

func TestVP8LPublicPhotoFixtureCompressionRegression(t *testing.T) {
	img := newVP8LPublicPhotoFixture(512, 512)
	data := encodeLosslessForTest(t, img, ModeDefault)
	const regressionLimit = 18918
	if len(data) >= regressionLimit {
		t.Fatalf("public photo fixture = %d bytes, want less than regression limit %d", len(data), regressionLimit)
	}
	assertVP8LRoundTrip(t, data, img)
}

func TestVP8LLocalMatchScreeningCompressionRegression(t *testing.T) {
	img := newLosslessBenchmarkFixtureImage(losslessBenchmarkCase{kind: benchmarkImageGradient, width: 128, height: 128})
	data := encodeLosslessForTest(t, img, ModeDefault)
	const regressionLimit = 384
	if len(data) >= regressionLimit {
		t.Fatalf("local-match gradient = %d bytes, want less than regression limit %d", len(data), regressionLimit)
	}
	assertVP8LRoundTrip(t, data, img)
}

func TestVP8LHuffmanCostMatchesEmissionTree(t *testing.T) {
	cases := [][]uint32{
		make([]uint32, nLiteralCodes+nLengthCodes),
		func() []uint32 {
			counts := make([]uint32, nLiteralCodes)
			counts[17] = 100
			return counts
		}(),
		func() []uint32 {
			counts := make([]uint32, nLiteralCodes)
			counts[17] = 40
			counts[219] = 60
			return counts
		}(),
		func() []uint32 {
			counts := make([]uint32, nLiteralCodes+nLengthCodes)
			for i := range counts {
				if i%7 != 0 {
					counts[i] = uint32(i%23 + 1)
				}
			}
			return counts
		}(),
		func() []uint32 {
			counts := make([]uint32, nLiteralCodes+nLengthCodes)
			counts[nLiteralCodes+3] = 100
			return counts
		}(),
	}
	workspace := &vp8lHuffmanWorkspace{}
	for i, counts := range cases {
		cost := buildVP8LHuffmanCostWorkspace(counts, workspace)
		tree := buildVP8LHuffmanTree(counts)
		want := tree.headerBits() + vp8lTreeDataBits(counts, &tree)
		if got := cost.headerBits + cost.dataBits; got != want {
			t.Fatalf("case %d cost bits = %d, emission tree %d", i, got, want)
		}
	}
}

func BenchmarkEncodeLosslessPublicPhoto512(b *testing.B) {
	img := newVP8LPublicPhotoFixture(512, 512)
	var output bytes.Buffer
	b.ReportAllocs()
	for b.Loop() {
		output.Reset()
		if err := encodeLossless(&output, newEncoderSource(img), ModeDefault); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(output.Len()), "encoded_B")
}

func newVP8LPublicPhotoFixture(width int, height int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			base := (x*37 + y*53 + x*y*3) & 0xff
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8((base + x/3 + y/5) & 0xff),
				G: uint8((base + x/7 + y/2 + 17) & 0xff),
				B: uint8((base + x/5 + y/11 + 41) & 0xff),
				A: 255,
			})
		}
	}
	return img
}
