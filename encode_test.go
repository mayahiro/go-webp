package webp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"testing"
)

func TestEncodeRoundTripNRGBA(t *testing.T) {
	img := image.NewNRGBA(image.Rect(10, 20, 12, 22))
	want := []color.NRGBA{
		{R: 1, G: 2, B: 3, A: 4},
		{R: 5, G: 6, B: 7, A: 8},
		{R: 9, G: 10, B: 11, A: 12},
		{R: 13, G: 14, B: 15, A: 16},
	}
	i := 0
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, want[i])
			i++
		}
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, nil); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	got, width, height, alpha, err := decodeEncoderOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != 2 || height != 2 {
		t.Fatalf("dimensions = %dx%d, want 2x2", width, height)
	}
	if !alpha {
		t.Fatal("alpha hint = false, want true")
	}
	if len(got) != len(want) {
		t.Fatalf("decoded pixel count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pixel %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestEncoderRoundTripGray(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 3, 1))
	img.SetGray(0, 0, color.Gray{Y: 7})
	img.SetGray(1, 0, color.Gray{Y: 7})
	img.SetGray(2, 0, color.Gray{Y: 9})

	var buf bytes.Buffer
	enc := Encoder{}
	if err := enc.Encode(&buf, img); err != nil {
		t.Fatalf("Encoder.Encode failed: %v", err)
	}

	got, width, height, alpha, err := decodeEncoderOutput(buf.Bytes())
	if err != nil {
		t.Fatalf("decodeEncoderOutput failed: %v", err)
	}
	if width != 3 || height != 1 {
		t.Fatalf("dimensions = %dx%d, want 3x1", width, height)
	}
	if alpha {
		t.Fatal("alpha hint = true, want false")
	}
	want := []color.NRGBA{
		{R: 7, G: 7, B: 7, A: 255},
		{R: 7, G: 7, B: 7, A: 255},
		{R: 9, G: 9, B: 9, A: 255},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pixel %d = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestEncodeRejectsInvalidInput(t *testing.T) {
	var buf bytes.Buffer
	if err := Encode(&buf, nil, nil); err == nil {
		t.Fatal("Encode with nil image succeeded")
	}
	if err := Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 0, 1)), nil); err == nil {
		t.Fatal("Encode with empty image succeeded")
	}
	if err := Encode(nil, image.NewNRGBA(image.Rect(0, 0, 1, 1)), nil); err == nil {
		t.Fatal("Encode with nil writer succeeded")
	}
}

func TestEncodePropagatesWriterError(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	err := Encode(failingWriter{}, img, nil)
	if !errors.Is(err, errFailingWriter) {
		t.Fatalf("Encode error = %v, want %v", err, errFailingWriter)
	}
}

var errFailingWriter = errors.New("writer failed")

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errFailingWriter
}

type decodedTree struct {
	constant bool
	symbol   uint8
}

func decodeEncoderOutput(data []byte) ([]color.NRGBA, int, int, bool, error) {
	if len(data) < 20 {
		return nil, 0, 0, false, errors.New("short WebP data")
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" || string(data[12:16]) != "VP8L" {
		return nil, 0, 0, false, errors.New("invalid WebP header")
	}
	riffSize := int(binary.LittleEndian.Uint32(data[4:8]))
	if riffSize+8 != len(data) {
		return nil, 0, 0, false, errors.New("invalid RIFF size")
	}
	payloadSize := int(binary.LittleEndian.Uint32(data[16:20]))
	if payloadSize < 0 || 20+payloadSize > len(data) {
		return nil, 0, 0, false, errors.New("invalid VP8L size")
	}
	if payloadSize%2 == 1 && data[20+payloadSize] != 0 {
		return nil, 0, 0, false, errors.New("invalid VP8L padding")
	}

	r := testBitReader{data: data[20 : 20+payloadSize]}
	signature, err := r.read(8)
	if err != nil {
		return nil, 0, 0, false, err
	}
	if signature != 0x2f {
		return nil, 0, 0, false, errors.New("invalid VP8L signature")
	}
	widthMinusOne, err := r.read(14)
	if err != nil {
		return nil, 0, 0, false, err
	}
	heightMinusOne, err := r.read(14)
	if err != nil {
		return nil, 0, 0, false, err
	}
	alphaHint, err := r.read(1)
	if err != nil {
		return nil, 0, 0, false, err
	}
	version, err := r.read(3)
	if err != nil {
		return nil, 0, 0, false, err
	}
	if version != 0 {
		return nil, 0, 0, false, errors.New("invalid VP8L version")
	}
	if v, err := r.read(1); err != nil || v != 0 {
		return nil, 0, 0, false, errors.New("unexpected transform")
	}
	if v, err := r.read(1); err != nil || v != 0 {
		return nil, 0, 0, false, errors.New("unexpected color cache")
	}
	if v, err := r.read(1); err != nil || v != 0 {
		return nil, 0, 0, false, errors.New("unexpected meta prefix image")
	}

	green, err := decodeEncoderTree(&r, nLiteralCodes+nLengthCodes)
	if err != nil {
		return nil, 0, 0, false, err
	}
	red, err := decodeEncoderTree(&r, nLiteralCodes)
	if err != nil {
		return nil, 0, 0, false, err
	}
	blue, err := decodeEncoderTree(&r, nLiteralCodes)
	if err != nil {
		return nil, 0, 0, false, err
	}
	alpha, err := decodeEncoderTree(&r, nLiteralCodes)
	if err != nil {
		return nil, 0, 0, false, err
	}
	distance, err := decodeEncoderTree(&r, nDistanceCodes)
	if err != nil {
		return nil, 0, 0, false, err
	}
	if !distance.constant || distance.symbol != 0 {
		return nil, 0, 0, false, errors.New("unexpected distance tree")
	}

	width, height := int(widthMinusOne+1), int(heightMinusOne+1)
	pixels := make([]color.NRGBA, width*height)
	for i := range pixels {
		g, err := decodeEncoderSymbol(&r, green)
		if err != nil {
			return nil, 0, 0, false, err
		}
		rr, err := decodeEncoderSymbol(&r, red)
		if err != nil {
			return nil, 0, 0, false, err
		}
		b, err := decodeEncoderSymbol(&r, blue)
		if err != nil {
			return nil, 0, 0, false, err
		}
		a, err := decodeEncoderSymbol(&r, alpha)
		if err != nil {
			return nil, 0, 0, false, err
		}
		pixels[i] = color.NRGBA{R: rr, G: g, B: b, A: a}
	}

	return pixels, width, height, alphaHint != 0, nil
}

func decodeEncoderTree(r *testBitReader, alphabetSize int) (decodedTree, error) {
	useSimple, err := r.read(1)
	if err != nil {
		return decodedTree{}, err
	}
	if useSimple != 0 {
		nSymbols, err := r.read(1)
		if err != nil {
			return decodedTree{}, err
		}
		if nSymbols != 0 {
			return decodedTree{}, errors.New("unexpected two-symbol tree")
		}
		use8Bits, err := r.read(1)
		if err != nil {
			return decodedTree{}, err
		}
		nBits := uint8(1)
		if use8Bits != 0 {
			nBits = 8
		}
		symbol, err := r.read(nBits)
		if err != nil {
			return decodedTree{}, err
		}
		if int(symbol) >= alphabetSize {
			return decodedTree{}, errors.New("simple tree symbol out of range")
		}
		return decodedTree{constant: true, symbol: uint8(symbol)}, nil
	}

	nCodes, err := r.read(4)
	if err != nil {
		return decodedTree{}, err
	}
	if nCodes != 8 {
		return decodedTree{}, errors.New("unexpected code length code count")
	}
	for _, want := range full8CodeLengthCodeLengths {
		got, err := r.read(3)
		if err != nil {
			return decodedTree{}, err
		}
		if got != uint32(want) {
			return decodedTree{}, errors.New("unexpected code length code")
		}
	}
	useLength, err := r.read(1)
	if err != nil {
		return decodedTree{}, err
	}
	if useLength != 0 {
		return decodedTree{}, errors.New("unexpected max symbol limit")
	}
	for symbol := 0; symbol < alphabetSize; symbol++ {
		got, err := r.read(1)
		if err != nil {
			return decodedTree{}, err
		}
		want := uint32(1)
		if symbol >= nLiteralCodes {
			want = 0
		}
		if got != want {
			return decodedTree{}, errors.New("unexpected code length")
		}
	}
	return decodedTree{}, nil
}

func decodeEncoderSymbol(r *testBitReader, tree decodedTree) (uint8, error) {
	if tree.constant {
		return tree.symbol, nil
	}
	v, err := r.read(8)
	if err != nil {
		return 0, err
	}
	return reverse8(uint8(v)), nil
}

type testBitReader struct {
	data  []byte
	off   int
	bits  uint64
	nBits uint8
}

func (r *testBitReader) read(n uint8) (uint32, error) {
	for r.nBits < n {
		if r.off >= len(r.data) {
			return 0, errors.New("unexpected end of VP8L data")
		}
		r.bits |= uint64(r.data[r.off]) << r.nBits
		r.nBits += 8
		r.off++
	}
	v := uint32(r.bits & uint64(1<<n-1))
	r.bits >>= n
	r.nBits -= n
	return v, nil
}
