package webp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"testing"
)

func TestEncodeStreamingStopsReadingAfterWriterError(t *testing.T) {
	img := image.NewNRGBA(image.Rect(-3, 5, 125, 133))
	state := uint32(0x12345678)
	for i := range img.Pix {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		img.Pix[i] = byte(state)
	}
	for _, mode := range []Mode{ModeFast, ModeLowMemory} {
		t.Run(modeNameForTest(mode), func(t *testing.T) {
			writer := &vp8lFailingImageWriter{}
			reader := &vp8lImageReadAfterError{Image: img, writer: writer}
			if err := Encode(writer, reader, &Options{Mode: mode}); !errors.Is(err, errBoundedWriter) {
				t.Fatalf("error = %v, want %v", err, errBoundedWriter)
			}
			if reader.readsAfterError != 0 {
				t.Fatalf("read %d pixels after writer failure", reader.readsAfterError)
			}
		})
	}
}

type vp8lFailingImageWriter struct {
	failed bool
}

func (w *vp8lFailingImageWriter) Write([]byte) (int, error) {
	w.failed = true
	return 0, errBoundedWriter
}

type vp8lImageReadAfterError struct {
	image.Image
	writer          *vp8lFailingImageWriter
	readsAfterError int
}

func (m *vp8lImageReadAfterError) At(x, y int) color.Color {
	if m.writer.failed {
		m.readsAfterError++
	}
	return m.Image.At(x, y)
}

func TestVP8LStreamingStopsReadingAfterWriterError(t *testing.T) {
	const width, height = 64, 64
	table := []uint32{0xff123456, 0xff789abc, 0x80456789, 0x00234567}
	for _, transform := range []string{"direct", "subtract-green", "predictor", "palette"} {
		for _, greedy := range []bool{false, true} {
			for _, limit := range []int{0, 128} {
				t.Run(fmt.Sprintf("%s/greedy=%t/limit=%d", transform, greedy, limit), func(t *testing.T) {
					writer := &vp8lFailingByteWriter{remaining: limit}
					readsAfterError := 0
					source := vp8lSource{width: width, height: height, readRow: func(y int, row []uint32) {
						if writer.failed {
							readsAfterError++
						}
						for x := range row {
							value := uint32(x+y*width+1) * 2654435761
							row[x] = table[value>>30]
						}
					}}
					stream := vp8lSourcePixelStream(source, nil)
					streamWidth := width
					switch transform {
					case "subtract-green":
						stream = vp8lSourcePixelStream(source, vp8lSubtractGreenPixel)
					case "predictor":
						stream = vp8lPredictorPixelStream(source, 12)
					case "palette":
						stream, streamWidth = vp8lPalettePixelStream(source, table)
					}
					tokens := vp8lLiteralTokenStream(stream)
					if greedy {
						tokens = vp8lGreedyTokenStream(stream, streamWidth)
					}
					group, dataBits := vp8lAnalyzeStreamingTokens(tokens)
					plan := newVP8LStreamingPlan(width, height, true, nil, group, dataBits, tokens)
					bits := vp8lBitWriter(writer)
					plan.writeTo(bits)
					if err := bits.flush(); !errors.Is(err, errBoundedWriter) {
						t.Fatalf("error = %v, want %v", err, errBoundedWriter)
					}
					if readsAfterError != 0 {
						t.Fatalf("read %d source rows after writer failure", readsAfterError)
					}
				})
			}
		}
	}
}

type vp8lFailingByteWriter struct {
	remaining int
	failed    bool
}

func TestVP8LGreedyStreamStopsAfterRejectedToken(t *testing.T) {
	for _, width := range []int{3, 8} {
		for _, stop := range []int{1, 2, 3} {
			read := 0
			stream := func(visit func(uint32) bool) {
				for _, pixel := range []uint32{1, 2, 2, 2, 2, 2, 2} {
					read++
					if !visit(pixel) {
						return
					}
				}
			}
			visited := 0
			vp8lGreedyTokenStream(stream, width)(func(vp8lToken) bool {
				visited++
				return visited < stop
			})
			if visited != stop {
				t.Errorf("width %d: visited %d tokens, want %d", width, visited, stop)
			}
			if width == 3 && read != width {
				t.Errorf("read %d pixels after rejecting a token in the first row", read)
			}
		}
	}
}

func (w *vp8lFailingByteWriter) WriteByte(byte) error {
	if w.remaining == 0 {
		w.failed = true
		return errBoundedWriter
	}
	w.remaining--
	return nil
}

func TestVP8LStreamingModesRoundTrip(t *testing.T) {
	img := image.NewNRGBA(image.Rect(-3, 5, 34, 28))
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			value := uint8((x*7 + y*13) & 31)
			img.SetNRGBA(x, y, color.NRGBA{
				R: value * 3,
				G: value * 5,
				B: value * 7,
				A: uint8((x*17 + y*29) & 0xff),
			})
		}
	}
	for _, mode := range []Mode{ModeFast, ModeLowMemory} {
		t.Run(modeName(mode), func(t *testing.T) {
			data := encodeLosslessForTest(t, img, mode)
			assertVP8LRoundTrip(t, data, img)
		})
	}
}

func TestVP8LStreamingNearLosslessMatchesPreprocessedPixels(t *testing.T) {
	img := newBenchmarkFixtureImage(lossyBenchmarkCase{kind: benchmarkImageGradient, width: 64, height: 64})
	source := newEncoderSource(img)
	const quality = 50
	readPixel := newNearLosslessReader(source, quality)
	var output bytes.Buffer
	if err := encodeNearLossless(&output, source, quality); err != nil {
		t.Fatal(err)
	}
	got, width, height, _, err := decodeEncoderOutput(output.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if width != source.width || height != source.height {
		t.Fatalf("dimensions = %dx%d, want %dx%d", width, height, source.width, source.height)
	}
	for y := range source.height {
		for x := range source.width {
			want := readPixel(source.bounds.Min.X+x, source.bounds.Min.Y+y)
			if got[y*source.width+x] != want {
				t.Fatalf("pixel (%d,%d) = %#v, want %#v", x, y, got[y*source.width+x], want)
			}
		}
	}
}

func TestVP8LBufferedSearchRejectsWorkspaceBeforeReadingSource(t *testing.T) {
	read := false
	source := vp8lSource{
		width:  maxVP8LDimension,
		height: maxVP8LDimension,
		readRow: func(int, []uint32) {
			read = true
		},
	}
	_, err := searchVP8L(source, vp8lBudgetForMode(ModeDefault))
	if !errors.Is(err, errVP8LSourceLimit) {
		t.Fatalf("searchVP8L error = %v, want workspace limit", err)
	}
	if read {
		t.Fatal("buffered search read the source before applying its workspace budget")
	}
}

func TestEncodeLosslessBalancedWorkspaceFallbackMatchesDefault(t *testing.T) {
	const width, height = 1400, 1000
	budget := vp8lBudgetForMode(ModeDefault)
	if vp8lBufferedSearchBytes(width*height, budget) <= budget.maxWorkspaceBytes {
		t.Fatal("fixture must exceed the buffered search workspace limit")
	}
	img := image.NewNRGBA(image.Rect(-3, 5, width-3, height+5))
	for y := range height {
		for x := range width {
			// A diagonal pattern benefits from predictors beyond left and above.
			value := uint32(x+y+1) * 2654435761
			img.SetNRGBA(img.Rect.Min.X+x, img.Rect.Min.Y+y, color.NRGBA{
				R: uint8(value >> 24), G: uint8(value >> 16), B: uint8(value >> 8), A: 255,
			})
		}
	}
	want := encodeLosslessForTest(t, img, ModeDefault)
	assertVP8LRoundTrip(t, want, img)
	for _, mode := range []Mode{ModeBalanced, ModeAuto} {
		t.Run(modeNameForTest(mode), func(t *testing.T) {
			got := encodeLosslessForTest(t, img, mode)
			if !bytes.Equal(got, want) {
				t.Errorf("fallback output = %d bytes, default = %d bytes", len(got), len(want))
			}
			assertVP8LRoundTrip(t, got, img)
		})
	}
}

func TestVP8LStreamingPlanPayloadBitsMatchEmission(t *testing.T) {
	img := newLosslessBenchmarkFixtureImage(losslessBenchmarkCase{kind: benchmarkImageUI, width: 41, height: 29})
	source := newEncoderSource(img)
	plan, err := searchVP8LStreaming(newVP8LSource(source, source.pixels()), ModeLowMemory)
	if err != nil {
		t.Fatal(err)
	}
	counter := vp8lBitCounter()
	plan.writeTo(counter)
	if counter.bitLen != plan.payloadBitLen() {
		t.Fatalf("counted bits = %d, plan bits %d", counter.bitLen, plan.payloadBitLen())
	}
	var output bytes.Buffer
	if err := writeLosslessVP8L(&output, plan); err != nil {
		t.Fatal(err)
	}
	payloadBytes := uint64(binary.LittleEndian.Uint32(output.Bytes()[16:20]))
	if want := (plan.payloadBitLen() + 7) / 8; payloadBytes != want {
		t.Fatalf("payload bytes = %d, want %d", payloadBytes, want)
	}
	assertVP8LRoundTrip(t, output.Bytes(), img)
}

func TestVP8LStreamingGreedyPlanCountsCopyDistanceBits(t *testing.T) {
	const width, height = 64, 8
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			value := uint8(x & 7)
			img.SetNRGBA(x, y, color.NRGBA{R: value, G: value * 3, B: value * 7, A: 255})
		}
	}
	source := newEncoderSource(img)
	stream := vp8lSourcePixelStream(newVP8LSource(source, source.pixels()), nil)
	tokens := vp8lGreedyTokenStream(stream, width)
	foundCopy := false
	tokens(func(token vp8lToken) bool {
		foundCopy = foundCopy || token.kind() == vp8lTokenCopy
		return true
	})
	if !foundCopy {
		t.Fatal("greedy streaming parser produced no copy")
	}
	group, dataBits := vp8lAnalyzeStreamingTokens(tokens)
	plan := newVP8LStreamingPlan(width, height, false, nil, group, dataBits, tokens)
	counter := vp8lBitCounter()
	plan.writeTo(counter)
	if counter.bitLen != plan.payloadBitLen() {
		t.Fatalf("counted bits = %d, plan bits %d", counter.bitLen, plan.payloadBitLen())
	}
	var output bytes.Buffer
	if err := writeLosslessVP8L(&output, plan); err != nil {
		t.Fatal(err)
	}
	assertVP8LRoundTrip(t, output.Bytes(), img)
}

func TestVP8LStreamingPredictorMatchesMaterializedTransform(t *testing.T) {
	img := newLosslessBenchmarkFixtureImage(losslessBenchmarkCase{kind: benchmarkImageGradient, width: 19, height: 13})
	source := newEncoderSource(img)
	vp8lSource := newVP8LSource(source, source.pixels())
	pixels, _, err := vp8lSource.materialize(vp8lMaxSourceBytes)
	if err != nil {
		t.Fatal(err)
	}
	for mode := uint8(0); mode < 14; mode++ {
		modes, transformWidth, _ := vp8lUniformPredictorImage(source.width, source.height, 9, mode)
		want := vp8lApplyPredictor(pixels, source.width, source.height, 9, modes, transformWidth)
		got := make([]uint32, 0, len(want))
		vp8lPredictorPixelStream(vp8lSource, mode)(func(pixel uint32) bool {
			got = append(got, pixel)
			return true
		})
		if !vp8lUint32SlicesEqual(got, want) {
			t.Fatalf("predictor mode %d changed in streaming path", mode)
		}
	}
}

func TestVP8LStreamingPaletteMatchesMaterializedTransform(t *testing.T) {
	const width, height = 23, 11
	img := image.NewPaletted(image.Rect(0, 0, width, height), color.Palette{
		color.NRGBA{R: 1, G: 2, B: 3, A: 255},
		color.NRGBA{R: 7, G: 11, B: 13, A: 127},
		color.NRGBA{R: 17, G: 19, B: 23, A: 0},
	})
	for i := range img.Pix {
		img.Pix[i] = uint8(i % len(img.Palette))
	}
	source := newEncoderSource(img)
	vp8lSource := newVP8LSource(source, source.pixels())
	table, ok := vp8lStreamingPalette(vp8lSource)
	if !ok {
		t.Fatal("streaming palette was not found")
	}
	pixels, _, err := vp8lSource.materialize(vp8lMaxSourceBytes)
	if err != nil {
		t.Fatal(err)
	}
	want, wantWidth := vp8lApplyPalette(pixels, width, height, table)
	stream, gotWidth := vp8lPalettePixelStream(vp8lSource, table)
	got := make([]uint32, 0, len(want))
	stream(func(pixel uint32) bool {
		got = append(got, pixel)
		return true
	})
	if gotWidth != wantWidth || !vp8lUint32SlicesEqual(got, want) {
		t.Fatalf("streaming palette = width %d pixels %v, want width %d pixels %v", gotWidth, got, wantWidth, want)
	}
}

func BenchmarkEncodeLosslessStreamingModes(b *testing.B) {
	img := newLosslessBenchmarkFixtureImage(losslessBenchmarkCase{kind: benchmarkImageUI, width: 128, height: 128})
	for _, mode := range []Mode{ModeFast, ModeLowMemory} {
		b.Run(modeName(mode), func(b *testing.B) {
			var output bytes.Buffer
			b.ReportAllocs()
			for b.Loop() {
				output.Reset()
				if err := encodeLossless(&output, newEncoderSource(img), mode); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(output.Len()), "encoded_B")
		})
	}
}

func modeName(mode Mode) string {
	switch mode {
	case ModeFast:
		return "fast"
	case ModeLowMemory:
		return "low-memory"
	default:
		return "other"
	}
}
