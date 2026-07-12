package webp

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEncodeLossyQualityRangeIsDeterministic(t *testing.T) {
	img := newLossyAcceptanceImage(false)
	for _, mode := range []Mode{ModeDefault, ModeFast, ModeBestCompression, ModeLowMemory} {
		for quality := 1; quality <= 100; quality++ {
			first := encodeLossyAcceptance(t, img, mode, quality)
			second := encodeLossyAcceptance(t, img, mode, quality)
			if !bytes.Equal(first, second) {
				t.Fatalf("mode %d quality %d output is not deterministic", mode, quality)
			}
			chunks := readWebPChunks(t, first)
			if len(chunks) != 1 || chunks[0].name != "VP8 " {
				t.Fatalf("mode %d quality %d chunks = %#v, want VP8", mode, quality, chunks)
			}
			assertLossyVP8Frame(t, chunks[0].payload, img.Rect.Dx(), img.Rect.Dy())
		}
	}
}

func TestEncodeLossyPublicModesWithAlphaAreDeterministic(t *testing.T) {
	img := newLossyAcceptanceImage(true)
	for _, mode := range []Mode{
		ModeDefault,
		ModeFast,
		ModeBalanced,
		ModeBestCompression,
		ModeLowMemory,
		ModeLossyQuality,
		ModeAuto,
	} {
		for _, quality := range []int{1, 25, 50, 75, 90, 100} {
			first := encodeLossyAcceptance(t, img, mode, quality)
			second := encodeLossyAcceptance(t, img, mode, quality)
			if !bytes.Equal(first, second) {
				t.Fatalf("mode %d quality %d alpha output is not deterministic", mode, quality)
			}
			chunks := readWebPChunks(t, first)
			if len(chunks) != 3 || chunks[0].name != "VP8X" || chunks[1].name != "ALPH" || chunks[2].name != "VP8 " {
				t.Fatalf("mode %d quality %d chunks = %#v, want VP8X, ALPH, VP8", mode, quality, chunks)
			}
			assertLossyVP8Frame(t, chunks[2].payload, img.Rect.Dx(), img.Rect.Dy())
		}
	}
}

func TestBestCompressionDoesNotExceedDefaultAcrossQualityRange(t *testing.T) {
	for _, img := range []*image.NRGBA{newLossyAcceptanceImage(false), newLossyAcceptanceImage(true)} {
		for quality := 1; quality <= 100; quality++ {
			defaultOutput := encodeLossyAcceptance(t, img, ModeDefault, quality)
			bestOutput := encodeLossyAcceptance(t, img, ModeBestCompression, quality)
			if len(bestOutput) > len(defaultOutput) {
				t.Fatalf("alpha=%t quality %d BestCompression = %d bytes, Default = %d", !img.Opaque(), quality, len(bestOutput), len(defaultOutput))
			}
		}
	}
}

func TestEncodeLossyQualityRangeHasNoLargeSizeReversal(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind benchmarkImageKind
	}{
		{name: "PhotoLike", kind: benchmarkImagePhotoLike},
		{name: "AlphaBands", kind: benchmarkImageAlphaBands},
	} {
		t.Run(tc.name, func(t *testing.T) {
			img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: tc.kind, width: 96, height: 80})
			previous := len(encodeLossyAcceptance(t, img, ModeDefault, 1))
			for quality := 2; quality <= 100; quality++ {
				current := len(encodeLossyAcceptance(t, img, ModeDefault, quality))
				maximumDrop := max(previous/20, 64)
				if previous-current > maximumDrop {
					t.Fatalf("quality %d size dropped from %d to %d bytes, maximum local drop %d", quality, previous, current, maximumDrop)
				}
				previous = current
			}
		})
	}
}

func TestEncodeLossyProfilesPreserveAlphaWithDWebP(t *testing.T) {
	if _, err := exec.LookPath("dwebp"); err != nil {
		t.Skip("dwebp is not available")
	}
	img := newLossyAcceptanceImage(true)
	for _, mode := range []Mode{ModeDefault, ModeFast, ModeBestCompression, ModeLowMemory} {
		for _, quality := range []int{1, 75, 100} {
			encoded := encodeLossyAcceptance(t, img, mode, quality)
			dir := t.TempDir()
			webpPath := filepath.Join(dir, "acceptance.webp")
			pngPath := filepath.Join(dir, "acceptance.png")
			if err := os.WriteFile(webpPath, encoded, 0o600); err != nil {
				t.Fatalf("mode %d quality %d write WebP: %v", mode, quality, err)
			}
			if output, err := exec.Command("dwebp", "-quiet", webpPath, "-o", pngPath).CombinedOutput(); err != nil {
				t.Fatalf("mode %d quality %d dwebp: %v: %s", mode, quality, err, output)
			}
			file, err := os.Open(pngPath)
			if err != nil {
				t.Fatalf("mode %d quality %d open PNG: %v", mode, quality, err)
			}
			decoded, decodeErr := png.Decode(file)
			closeErr := file.Close()
			if decodeErr != nil {
				t.Fatalf("mode %d quality %d decode PNG: %v", mode, quality, decodeErr)
			}
			if closeErr != nil {
				t.Fatalf("mode %d quality %d close PNG: %v", mode, quality, closeErr)
			}
			if decoded.Bounds().Dx() != img.Rect.Dx() || decoded.Bounds().Dy() != img.Rect.Dy() {
				t.Fatalf("mode %d quality %d dimensions = %dx%d, want %dx%d", mode, quality, decoded.Bounds().Dx(), decoded.Bounds().Dy(), img.Rect.Dx(), img.Rect.Dy())
			}
			for y := 0; y < img.Rect.Dy(); y++ {
				for x := 0; x < img.Rect.Dx(); x++ {
					_, _, _, alpha := decoded.At(x, y).RGBA()
					if got, want := uint8(alpha>>8), img.NRGBAAt(img.Rect.Min.X+x, img.Rect.Min.Y+y).A; got != want {
						t.Fatalf("mode %d quality %d alpha (%d,%d) = %d, want %d", mode, quality, x, y, got, want)
					}
				}
			}
		}
	}
}

func encodeLossyAcceptance(t *testing.T, img image.Image, mode Mode, quality int) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := Encode(&output, img, &Options{Compression: CompressionLossy, Mode: mode, Quality: quality}); err != nil {
		t.Fatalf("mode %d quality %d Encode: %v", mode, quality, err)
	}
	return output.Bytes()
}

func newLossyAcceptanceImage(withAlpha bool) *image.NRGBA {
	rect := image.Rect(3, 5, 34, 28)
	img := image.NewNRGBA(rect)
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			alpha := uint8(255)
			if withAlpha {
				alpha = uint8(32 + (x*17+y*29)%224)
			}
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*11 + y*7),
				G: uint8(x*3 + y*19),
				B: uint8(x*y + x*13 - y*5),
				A: alpha,
			})
		}
	}
	return img
}
