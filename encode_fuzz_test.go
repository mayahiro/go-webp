package webp

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"io"
	"testing"

	"github.com/mayahiro/go-webp/internal/benchmarkbitstream"
)

func FuzzEncodePublicAPI(f *testing.F) {
	seeds := []struct {
		imageKind  uint8
		mode       Mode
		lossy      bool
		quality    uint8
		width      uint8
		height     uint8
		originX    int8
		originY    int8
		padding    uint8
		opaque     bool
		pixelBytes []byte
	}{
		{imageKind: 0, mode: ModeDefault, quality: 100, width: 1, height: 1, opaque: true, pixelBytes: []byte{1, 2, 3, 255}},
		{imageKind: 1, mode: ModeFast, lossy: true, quality: 1, width: 3, height: 5, originX: -2, originY: 3, padding: 7, pixelBytes: []byte{7, 11, 13, 17}},
		{imageKind: 2, mode: ModeBalanced, quality: 50, width: 5, height: 3, originX: 2, originY: -3, padding: 3, opaque: true, pixelBytes: []byte{23, 29}},
		{imageKind: 3, mode: ModeBestCompression, lossy: true, quality: 75, width: 7, height: 5, originX: -1, originY: -1, pixelBytes: []byte{31, 37, 41}},
		{imageKind: 4, mode: ModeLowMemory, quality: 100, width: 5, height: 7, originX: 3, originY: 2, pixelBytes: []byte{43, 47, 53, 59}},
		{imageKind: 0, mode: ModeNearLossless, quality: 75, width: 7, height: 7, originX: -3, originY: 1, padding: 5, pixelBytes: []byte{61, 67, 71, 73}},
		{imageKind: 1, mode: ModeLossyQuality, quality: 100, width: 3, height: 7, originX: 1, originY: -2, padding: 9, pixelBytes: []byte{79, 83, 89, 97}},
		{imageKind: 4, mode: ModeAuto, lossy: true, quality: 25, width: 7, height: 3, originX: -2, originY: 2, pixelBytes: []byte{101, 103, 107, 109}},
	}
	for _, seed := range seeds {
		f.Add(seed.imageKind, uint8(seed.mode), seed.lossy, seed.quality, seed.width, seed.height, seed.originX, seed.originY, seed.padding, seed.opaque, seed.pixelBytes)
	}

	f.Fuzz(func(t *testing.T, imageKind uint8, rawMode uint8, lossy bool, rawQuality uint8, rawWidth uint8, rawHeight uint8, rawOriginX int8, rawOriginY int8, padding uint8, opaque bool, pixelBytes []byte) {
		modes := [...]Mode{
			ModeDefault,
			ModeFast,
			ModeBalanced,
			ModeBestCompression,
			ModeLowMemory,
			ModeNearLossless,
			ModeLossyQuality,
			ModeAuto,
		}
		mode := modes[int(rawMode)%len(modes)]
		width := int(rawWidth%8) + 1
		height := int(rawHeight%8) + 1
		originX := int(rawOriginX%4) - 2
		originY := int(rawOriginY%4) - 2
		img := fuzzImage(imageKind%5, image.Rect(originX, originY, originX+width, originY+height), int(padding%12), opaque, pixelBytes)
		opts := &Options{
			Mode:    mode,
			Quality: int(rawQuality%100) + 1,
		}
		if lossy {
			opts.Compression = CompressionLossy
		}

		first := encodeFuzzImage(t, img, opts)
		second := encodeFuzzImage(t, img, opts)
		if !bytes.Equal(second, first) {
			t.Fatalf("non-deterministic output: first=%d bytes second=%d bytes", len(first), len(second))
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var cancellable bytes.Buffer
		if err := EncodeContext(ctx, &cancellable, img, opts); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(cancellable.Bytes(), first) {
			t.Fatal("cancellable encoding changed the output")
		}
		readImage := &contextReadImage{Image: img, at: int(rawQuality)%(width*height) + 1, onAt: cancel}
		if err := EncodeContext(ctx, io.Discard, readImage, opts); err != context.Canceled {
			t.Fatalf("cancelled encode error = %v", err)
		}
		validateFuzzWebP(t, first, img.Bounds().Dx(), img.Bounds().Dy())
	})
}

func TestEncodePropagatesBoundedWriterFailuresForPublicModes(t *testing.T) {
	img := fuzzImage(0, image.Rect(-2, 3, 15, 18), 7, false, []byte{1, 3, 5, 7, 11, 13})
	for _, mode := range []Mode{
		ModeDefault,
		ModeFast,
		ModeBalanced,
		ModeBestCompression,
		ModeLowMemory,
		ModeNearLossless,
		ModeLossyQuality,
		ModeAuto,
	} {
		t.Run(modeNameForTest(mode), func(t *testing.T) {
			opts := &Options{Mode: mode, Quality: 75}
			if mode != ModeNearLossless {
				opts.Compression = CompressionLossy
			}
			encoded := encodeFuzzImage(t, img, opts)
			for _, limit := range []int{0, len(encoded) / 2, len(encoded) - 1} {
				writer := &boundedErrorWriter{remaining: limit}
				err := Encode(writer, img, opts)
				if !errors.Is(err, errBoundedWriter) {
					t.Fatalf("limit %d error = %v, want %v", limit, err, errBoundedWriter)
				}
			}
		})
	}
}

func TestEncodeLosslessPropagatesMidstreamWriterFailures(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	state := uint32(0x12345678)
	for i := range img.Pix {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		img.Pix[i] = uint8(state)
	}
	for _, mode := range []Mode{
		ModeDefault,
		ModeFast,
		ModeBalanced,
		ModeBestCompression,
		ModeLowMemory,
		ModeNearLossless,
		ModeAuto,
	} {
		t.Run(modeNameForTest(mode), func(t *testing.T) {
			opts := &Options{Mode: mode, Quality: 75}
			encoded := encodeFuzzImage(t, img, opts)
			// Force writes before the final Flush of the 4 KiB output buffer.
			if len(encoded) <= 8192 {
				t.Fatalf("encoded size = %d, want more than 8192 bytes", len(encoded))
			}
			for _, limit := range []int{0, 4096, len(encoded) / 2, len(encoded) - 1} {
				writer := &boundedErrorWriter{remaining: limit}
				if err := Encode(writer, img, opts); !errors.Is(err, errBoundedWriter) {
					t.Errorf("limit %d error = %v, want %v", limit, err, errBoundedWriter)
				}
			}
		})
	}
}

func fuzzImage(kind uint8, bounds image.Rectangle, padding int, opaque bool, data []byte) image.Image {
	width, height := bounds.Dx(), bounds.Dy()
	switch kind {
	case 0:
		img := &image.NRGBA{Pix: make([]byte, (width*4+padding)*height), Stride: width*4 + padding, Rect: bounds}
		fillFuzzImage(img, bounds, opaque, data)
		return img
	case 1:
		img := &image.RGBA{Pix: make([]byte, (width*4+padding)*height), Stride: width*4 + padding, Rect: bounds}
		fillFuzzImage(img, bounds, opaque, data)
		return img
	case 2:
		img := &image.Gray{Pix: make([]byte, (width+padding)*height), Stride: width + padding, Rect: bounds}
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				img.SetGray(x, y, color.Gray{Y: fuzzByte(data, (y-bounds.Min.Y)*width+x-bounds.Min.X)})
			}
		}
		return img
	case 3:
		bounds = image.Rect(absForFuzz(bounds.Min.X)+1, absForFuzz(bounds.Min.Y)+1, absForFuzz(bounds.Min.X)+1+width, absForFuzz(bounds.Min.Y)+1+height)
		img := image.NewYCbCr(bounds, image.YCbCrSubsampleRatio420)
		for index := range img.Y {
			img.Y[index] = fuzzByte(data, index)
		}
		for index := range img.Cb {
			img.Cb[index] = fuzzByte(data, index+len(img.Y))
			img.Cr[index] = fuzzByte(data, index+len(img.Y)+len(img.Cb))
		}
		return img
	default:
		palette := make(color.Palette, 16)
		for index := range palette {
			alpha := fuzzAlpha(opaque, index)
			palette[index] = color.NRGBA{
				R: fuzzByte(data, index*4),
				G: fuzzByte(data, index*4+1),
				B: fuzzByte(data, index*4+2),
				A: alpha,
			}
		}
		img := image.NewPaletted(bounds, palette)
		for index := range img.Pix {
			img.Pix[index] = fuzzByte(data, index) & 15
		}
		return img
	}
}

func absForFuzz(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func fillFuzzImage(img interface{ Set(int, int, color.Color) }, bounds image.Rectangle, opaque bool, data []byte) {
	width := bounds.Dx()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			index := (y-bounds.Min.Y)*width + x - bounds.Min.X
			img.Set(x, y, color.NRGBA{
				R: fuzzByte(data, index*4),
				G: fuzzByte(data, index*4+1),
				B: fuzzByte(data, index*4+2),
				A: fuzzAlpha(opaque, index),
			})
		}
	}
}

func fuzzByte(data []byte, index int) uint8 {
	if len(data) == 0 {
		return uint8(index*37 + 17)
	}
	return data[index%len(data)]
}

func fuzzAlpha(opaque bool, index int) uint8 {
	if opaque {
		return 255
	}
	return uint8(index*53 + 127)
}

func encodeFuzzImage(t *testing.T, img image.Image, opts *Options) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := Encode(&output, img, opts); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func validateFuzzWebP(t *testing.T, data []byte, width int, height int) {
	t.Helper()
	chunks := readWebPChunks(t, data)
	if len(chunks) == 0 {
		t.Fatal("WebP contains no chunks")
	}
	switch chunks[len(chunks)-1].name {
	case "VP8L":
		if len(chunks) != 1 {
			t.Fatalf("VP8L chunk sequence = %#v", chunks)
		}
		layout, err := benchmarkbitstream.ParseLossless(data)
		if err != nil {
			t.Fatalf("VP8L structure: %v", err)
		}
		if layout.Width != width || layout.Height != height {
			t.Fatalf("VP8L dimensions = %dx%d, want %dx%d", layout.Width, layout.Height, width, height)
		}
	case "VP8 ":
		if len(chunks) == 1 {
			assertLossyVP8Frame(t, chunks[0].payload, width, height)
		} else {
			if len(chunks) != 3 || chunks[0].name != "VP8X" || chunks[1].name != "ALPH" {
				t.Fatalf("extended lossy chunk sequence = %#v", chunks)
			}
			validateFuzzVP8X(t, chunks[0].payload, width, height)
			validateFuzzALPH(t, chunks[1].payload)
			assertLossyVP8Frame(t, chunks[2].payload, width, height)
		}
		if _, err := benchmarkbitstream.ParseLossy(data); err != nil {
			t.Fatalf("VP8 structure: %v", err)
		}
	default:
		t.Fatalf("last WebP chunk = %q", chunks[len(chunks)-1].name)
	}
}

func validateFuzzVP8X(t *testing.T, payload []byte, width int, height int) {
	t.Helper()
	if len(payload) != vp8xPayloadSize {
		t.Fatalf("VP8X payload size = %d", len(payload))
	}
	if payload[0] != vp8xAlphaFlag || payload[1] != 0 || payload[2] != 0 || payload[3] != 0 {
		t.Fatalf("VP8X flags and reserved bytes = % x", payload[:4])
	}
	if got := readUint24LE(payload[4:7]) + 1; got != width {
		t.Fatalf("VP8X width = %d, want %d", got, width)
	}
	if got := readUint24LE(payload[7:10]) + 1; got != height {
		t.Fatalf("VP8X height = %d, want %d", got, height)
	}
}

func validateFuzzALPH(t *testing.T, payload []byte) {
	t.Helper()
	if len(payload) < 2 {
		t.Fatalf("ALPH payload size = %d", len(payload))
	}
	header := payload[0]
	if header&0xf0 != 0 || header&0x03 > alphCompressionVP8L || header>>2&0x03 > alphFilterGradient {
		t.Fatalf("invalid ALPH header = %#02x", header)
	}
}

func modeNameForTest(mode Mode) string {
	return [...]string{"default", "fast", "balanced", "best", "low-memory", "near-lossless", "lossy-quality", "auto"}[mode]
}

var errBoundedWriter = errors.New("bounded writer failed")

type boundedErrorWriter struct {
	remaining int
}

func (w *boundedErrorWriter) Write(data []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, errBoundedWriter
	}
	if len(data) <= w.remaining {
		w.remaining -= len(data)
		return len(data), nil
	}
	written := w.remaining
	w.remaining = 0
	return written, errBoundedWriter
}

var _ io.Writer = (*boundedErrorWriter)(nil)
