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

func TestEncodeLossyWritesVP8Chunk(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 17, 19))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x * 11),
				G: uint8(y * 9),
				B: uint8((x + y) * 5),
				A: 255,
			})
		}
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{Compression: CompressionLossy}); err != nil {
		t.Fatalf("Encode lossy failed: %v", err)
	}

	data := buf.Bytes()
	if len(data) < 30 {
		t.Fatalf("lossy WebP length = %d, want at least 30", len(data))
	}
	chunks := readWebPChunks(t, data)
	if len(chunks) != 1 {
		t.Fatalf("chunk count = %d, want 1", len(chunks))
	}
	if chunks[0].name != "VP8 " {
		t.Fatalf("chunk name = %q, want VP8 ", chunks[0].name)
	}
	assertLossyVP8Frame(t, chunks[0].payload, 17, 19)
}

func TestEncodeLossyQualityOption(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 17, 19))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*11 + y*3),
				G: uint8(y*9 + x*5),
				B: uint8((x + y) * 7),
				A: 255,
			})
		}
	}

	encode := func(quality int) []byte {
		t.Helper()
		var buf bytes.Buffer
		if err := Encode(&buf, img, &Options{
			Compression: CompressionLossy,
			Quality:     quality,
		}); err != nil {
			t.Fatalf("Encode lossy quality %d failed: %v", quality, err)
		}
		return buf.Bytes()
	}

	defaultQuality := encode(0)
	quality100 := encode(100)
	quality1 := encode(1)
	qualityOverMax := encode(200)
	qualityNegative := encode(-1)

	if !bytes.Equal(defaultQuality, quality100) {
		t.Fatal("default lossy quality differs from Quality 100")
	}
	if !bytes.Equal(qualityOverMax, quality100) {
		t.Fatal("Quality greater than 100 was not clamped to Quality 100")
	}
	if !bytes.Equal(qualityNegative, quality100) {
		t.Fatal("Quality less than or equal to zero did not use the default")
	}
	if bytes.Equal(quality1, quality100) {
		t.Fatal("Quality 1 output equals Quality 100 output")
	}

	chunks := readWebPChunks(t, quality1)
	if len(chunks) != 1 {
		t.Fatalf("chunk count = %d, want 1", len(chunks))
	}
	if chunks[0].name != "VP8 " {
		t.Fatalf("chunk name = %q, want VP8 ", chunks[0].name)
	}
	assertLossyVP8Frame(t, chunks[0].payload, 17, 19)
}

func TestEncodeLossyEnablesSimpleLoopFilter(t *testing.T) {
	const quality = 25

	img := image.NewNRGBA(image.Rect(0, 0, 17, 19))
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(x*13 + y*3),
				G: uint8(y*11 + x*5),
				B: uint8((x + y) * 7),
				A: 255,
			})
		}
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{
		Compression: CompressionLossy,
		Quality:     quality,
	}); err != nil {
		t.Fatalf("Encode lossy failed: %v", err)
	}

	chunks := readWebPChunks(t, buf.Bytes())
	if len(chunks) != 1 {
		t.Fatalf("chunk count = %d, want 1", len(chunks))
	}
	if chunks[0].name != "VP8 " {
		t.Fatalf("chunk name = %q, want VP8 ", chunks[0].name)
	}

	got := readVP8LoopFilterHeader(t, chunks[0].payload)
	want := vp8LoopFilterForIndex(qualityToVP8QIndex(quality))
	if got != want {
		t.Fatalf("loop filter = %#v, want %#v", got, want)
	}
	if got.level == 0 {
		t.Fatal("loop filter level = 0, want enabled")
	}
}

func TestVP8BlockQuantizationKeepsAC(t *testing.T) {
	residual := [16]int{
		64, -64, 64, -64,
		-64, 64, -64, 64,
		64, -64, 64, -64,
		-64, 64, -64, 64,
	}
	quant := vp8QuantForIndex(qualityToVP8QIndex(100))
	coeff := quantizeVP8Block(residual, quant.y1DC, quant.y1AC)

	hasAC := false
	for _, c := range coeff[1:] {
		if c != 0 {
			hasAC = true
			break
		}
	}
	if !hasAC {
		t.Fatal("quantized checkerboard residual has no AC coefficients")
	}

	recon := reconstructVP8Block(filledBlock4(128), coeff, quant.y1DC, quant.y1AC)
	minV, maxV := recon[0], recon[0]
	for _, v := range recon[1:] {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}
	if minV == maxV {
		t.Fatal("AC reconstruction produced a constant block")
	}
}

func TestVP8Y16ModeSelectionChoosesVertical(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 32))
	recY := make([]uint8, 16*32)
	for x := 0; x < 16; x++ {
		v := uint8(32 + x*8)
		recY[15*16+x] = v
		for y := 16; y < 32; y++ {
			img.SetNRGBA(x, y, color.NRGBA{R: v, G: v, B: v, A: 255})
		}
	}

	quant := vp8QuantForIndex(qualityToVP8QIndex(75))
	rd := newVP8RDConfig(quant)
	var left, up [4]uint8
	var leftY16, upY16 uint8
	mode, score := chooseVP8Y16Mode(pixelReaderFor(img), img.Bounds(), 0, 1, recY, 16, quant, rd, &left, &up, &leftY16, &upY16)
	if mode != vp8PredVE {
		t.Fatalf("Y16 mode = %d, want vertical", mode)
	}
	var zero [16]int
	wantBits := vp8BitCost(145, true) + vp8Y16ModeCost(vp8PredVE) + vp8BlockBitCost(vp8PlaneY2, 0, zero)
	for i := 0; i < 16; i++ {
		wantBits += vp8BlockBitCostFrom(vp8PlaneY1WithY2, 0, zero, 1)
	}
	if want := rd.lumaScore(0, wantBits); score != want {
		t.Fatalf("Y16 vertical score = %d, want %d", score, want)
	}
}

func TestVP8Y4ModeSelectionChoosesVertical(t *testing.T) {
	const stride = 16
	const x = 4
	const y = 4

	recY := make([]uint8, stride*16)
	top := []uint8{0, 0, 0, 255, 255, 255}
	for i, v := range top {
		recY[(y-1)*stride+x-1+i] = v
	}
	for yy := 0; yy < 4; yy++ {
		recY[(y+yy)*stride+x-1] = 220
	}
	pred := predictLuma4(recY, stride, x, y, vp8PredVE)

	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			v := pred[yy*4+xx]
			img.SetNRGBA(x+xx, y+yy, color.NRGBA{R: v, G: v, B: v, A: 255})
		}
	}

	quant := vp8QuantForIndex(qualityToVP8QIndex(1))
	rd := newVP8RDConfig(quant)
	mode, score, nz := chooseVP8Y4Mode(pixelReaderFor(img), img.Bounds(), x, y, recY, stride, quant, rd, vp8PredVE, vp8PredVE, 0)
	if mode != vp8PredVE {
		t.Fatalf("Y4 mode = %d, want vertical", mode)
	}
	if nz != 0 {
		t.Fatalf("Y4 vertical nz = %d, want 0", nz)
	}
	var zero [16]int
	wantBits := vp8Y4ModeCost(vp8PredVE, vp8PredVE, vp8PredVE) + vp8BlockBitCost(vp8PlaneY1SansY2, 0, zero)
	if want := rd.lumaScore(0, wantBits); score != want {
		t.Fatalf("Y4 vertical score = %d, want %d", score, want)
	}
}

func TestVP8FirstPartitionWritesSelectedY4Modes(t *testing.T) {
	want := [16]uint8{
		vp8PredDC, vp8PredTM, vp8PredVE, vp8PredHE,
		vp8PredRD, vp8PredVR, vp8PredLD, vp8PredVL,
		vp8PredHD, vp8PredHU, vp8PredDC, vp8PredTM,
		vp8PredVE, vp8PredHE, vp8PredRD, vp8PredVR,
	}
	firstPart, err := vp8FirstPartition(1, 1, qualityToVP8QIndex(75), vp8LoopFilterForIndex(qualityToVP8QIndex(75)), []vp8MBMode{{
		y4Modes: want,
		cMode:   vp8PredDC,
	}})
	if err != nil {
		t.Fatalf("vp8FirstPartition failed: %v", err)
	}

	got := readVP8FirstPartitionY4Modes(t, firstPart)
	if got != want {
		t.Fatalf("Y4 modes = %v, want %v", got, want)
	}
}

func TestVP8BlockBitCostAccountsForNonZeroCoefficients(t *testing.T) {
	var zero [16]int
	var dc [16]int
	dc[0] = 1
	zeroCost := vp8BlockBitCost(vp8PlaneY1SansY2, 0, zero)
	if got := vp8BlockBitCost(vp8PlaneY1SansY2, 0, dc); got <= zeroCost {
		t.Fatalf("non-zero DC bit cost = %d, want greater than zero block cost %d", got, zeroCost)
	}

	var ac [16]int
	ac[1] = 1
	zeroSkipCost := vp8BlockBitCostFrom(vp8PlaneY1WithY2, 0, zero, 1)
	if got := vp8BlockBitCostFrom(vp8PlaneY1WithY2, 0, ac, 1); got <= zeroSkipCost {
		t.Fatalf("non-zero AC bit cost = %d, want greater than zero skip-first cost %d", got, zeroSkipCost)
	}
}

func TestVP8RDLambdaIncreasesWithQuantizer(t *testing.T) {
	highQuality := newVP8RDConfig(vp8QuantForIndex(qualityToVP8QIndex(90)))
	lowQuality := newVP8RDConfig(vp8QuantForIndex(qualityToVP8QIndex(10)))
	if lowQuality.yLambda <= highQuality.yLambda {
		t.Fatalf("low quality luma lambda = %d, want greater than high quality lambda %d", lowQuality.yLambda, highQuality.yLambda)
	}
	if lowQuality.uvLambda <= highQuality.uvLambda {
		t.Fatalf("low quality chroma lambda = %d, want greater than high quality lambda %d", lowQuality.uvLambda, highQuality.uvLambda)
	}
}

func TestEncodeLossyWithAlphaWritesExtendedChunks(t *testing.T) {
	img := image.NewNRGBA(image.Rect(4, 5, 7, 7))
	wantAlpha := []byte{255, 128, 0, 64, 200, 255}
	i := 0
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(20 + i*7),
				G: uint8(40 + i*9),
				B: uint8(60 + i*11),
				A: wantAlpha[i],
			})
			i++
		}
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{Compression: CompressionLossy}); err != nil {
		t.Fatalf("Encode lossy with alpha failed: %v", err)
	}

	chunks := readWebPChunks(t, buf.Bytes())
	if len(chunks) != 3 {
		t.Fatalf("chunk count = %d, want 3", len(chunks))
	}
	if chunks[0].name != "VP8X" {
		t.Fatalf("first chunk = %q, want VP8X", chunks[0].name)
	}
	if len(chunks[0].payload) != vp8xPayloadSize {
		t.Fatalf("VP8X payload size = %d, want %d", len(chunks[0].payload), vp8xPayloadSize)
	}
	if chunks[0].payload[0] != vp8xAlphaFlag {
		t.Fatalf("VP8X flags = %#02x, want %#02x", chunks[0].payload[0], vp8xAlphaFlag)
	}
	if !bytes.Equal(chunks[0].payload[1:4], []byte{0, 0, 0}) {
		t.Fatalf("VP8X reserved bytes = % x, want 00 00 00", chunks[0].payload[1:4])
	}
	if widthMinusOne := readUint24LE(chunks[0].payload[4:7]); widthMinusOne != 2 {
		t.Fatalf("VP8X width minus one = %d, want 2", widthMinusOne)
	}
	if heightMinusOne := readUint24LE(chunks[0].payload[7:10]); heightMinusOne != 1 {
		t.Fatalf("VP8X height minus one = %d, want 1", heightMinusOne)
	}

	if chunks[1].name != "ALPH" {
		t.Fatalf("second chunk = %q, want ALPH", chunks[1].name)
	}
	if len(chunks[1].payload) != 1+len(wantAlpha) {
		t.Fatalf("ALPH payload size = %d, want %d", len(chunks[1].payload), 1+len(wantAlpha))
	}
	if chunks[1].payload[0] != 0 {
		t.Fatalf("ALPH header = %#02x, want 0", chunks[1].payload[0])
	}
	if !bytes.Equal(chunks[1].payload[1:], wantAlpha) {
		t.Fatalf("ALPH data = %v, want %v", chunks[1].payload[1:], wantAlpha)
	}

	if chunks[2].name != "VP8 " {
		t.Fatalf("third chunk = %q, want VP8 ", chunks[2].name)
	}
	assertLossyVP8Frame(t, chunks[2].payload, 3, 2)
}

func TestEncodeLossyWithFilteredAlphaWritesCompressedALPH(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 12, 1))
	for x := 0; x < img.Rect.Dx(); x++ {
		img.SetNRGBA(x, 0, color.NRGBA{
			R: uint8(10 + x),
			G: uint8(20 + x),
			B: uint8(30 + x),
			A: uint8((x + 1) * 7),
		})
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{Compression: CompressionLossy}); err != nil {
		t.Fatalf("Encode lossy with filtered alpha failed: %v", err)
	}

	chunks := readWebPChunks(t, buf.Bytes())
	if len(chunks) != 3 {
		t.Fatalf("chunk count = %d, want 3", len(chunks))
	}
	if chunks[1].name != "ALPH" {
		t.Fatalf("second chunk = %q, want ALPH", chunks[1].name)
	}
	if chunks[1].payload[0]&0x03 != alphCompressionVP8L {
		t.Fatalf("ALPH compression = %d, want %d", chunks[1].payload[0]&0x03, alphCompressionVP8L)
	}
	if chunks[1].payload[0]>>2&0x03 != alphFilterHorizontal {
		t.Fatalf("ALPH filter = %d, want %d", chunks[1].payload[0]>>2&0x03, alphFilterHorizontal)
	}
	if len(chunks[1].payload) >= 1+img.Rect.Dx()*img.Rect.Dy() {
		t.Fatalf("compressed ALPH payload size = %d, want smaller than raw %d", len(chunks[1].payload), 1+img.Rect.Dx()*img.Rect.Dy())
	}
	assertLossyVP8Frame(t, chunks[2].payload, 12, 1)
}

func TestEncodeLossyWithBinaryAlphaWritesCompressedALPH(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 16, 1))
	for x := 0; x < img.Rect.Dx(); x++ {
		alpha := uint8(0)
		if x%2 == 0 {
			alpha = 255
		}
		img.SetNRGBA(x, 0, color.NRGBA{
			R: uint8(100 + x),
			G: uint8(80 + x),
			B: uint8(60 + x),
			A: alpha,
		})
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{Compression: CompressionLossy}); err != nil {
		t.Fatalf("Encode lossy with binary alpha failed: %v", err)
	}

	chunks := readWebPChunks(t, buf.Bytes())
	if len(chunks) != 3 {
		t.Fatalf("chunk count = %d, want 3", len(chunks))
	}
	if chunks[1].name != "ALPH" {
		t.Fatalf("second chunk = %q, want ALPH", chunks[1].name)
	}
	if chunks[1].payload[0]&0x03 != alphCompressionVP8L {
		t.Fatalf("ALPH compression = %d, want %d", chunks[1].payload[0]&0x03, alphCompressionVP8L)
	}
	if chunks[1].payload[0]>>2&0x03 != alphFilterNone {
		t.Fatalf("ALPH filter = %d, want %d", chunks[1].payload[0]>>2&0x03, alphFilterNone)
	}
	if len(chunks[1].payload) >= 1+img.Rect.Dx()*img.Rect.Dy() {
		t.Fatalf("compressed ALPH payload size = %d, want smaller than raw %d", len(chunks[1].payload), 1+img.Rect.Dx()*img.Rect.Dy())
	}
	assertLossyVP8Frame(t, chunks[2].payload, 16, 1)
}

func TestEncodeLossyWithMultiSymbolAlphaWritesCompressedALPH(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 1024, 1))
	alphaValues := [...]uint8{0, 100, 200}
	for x := 0; x < img.Rect.Dx(); x++ {
		alpha := alphaValues[x%len(alphaValues)]
		img.SetNRGBA(x, 0, color.NRGBA{
			R: uint8(x),
			G: uint8(x >> 1),
			B: uint8(x >> 2),
			A: alpha,
		})
	}

	var buf bytes.Buffer
	if err := Encode(&buf, img, &Options{Compression: CompressionLossy}); err != nil {
		t.Fatalf("Encode lossy with multi-symbol alpha failed: %v", err)
	}

	chunks := readWebPChunks(t, buf.Bytes())
	if len(chunks) != 3 {
		t.Fatalf("chunk count = %d, want 3", len(chunks))
	}
	if chunks[1].name != "ALPH" {
		t.Fatalf("second chunk = %q, want ALPH", chunks[1].name)
	}
	if chunks[1].payload[0]&0x03 != alphCompressionVP8L {
		t.Fatalf("ALPH compression = %d, want %d", chunks[1].payload[0]&0x03, alphCompressionVP8L)
	}
	if len(chunks[1].payload) >= 1+img.Rect.Dx()*img.Rect.Dy() {
		t.Fatalf("compressed ALPH payload size = %d, want smaller than raw %d", len(chunks[1].payload), 1+img.Rect.Dx()*img.Rect.Dy())
	}
	assertLossyVP8Frame(t, chunks[2].payload, 1024, 1)
}

func assertLossyVP8Frame(t *testing.T, frame []byte, wantWidth int, wantHeight int) {
	t.Helper()
	if len(frame) < 10 {
		t.Fatalf("VP8 frame length = %d, want at least 10", len(frame))
	}
	frameTag := uint32(frame[0]) | uint32(frame[1])<<8 | uint32(frame[2])<<16
	if frameTag&1 != 0 {
		t.Fatal("VP8 frame is not a key frame")
	}
	if frameTag>>4&1 != 1 {
		t.Fatal("VP8 frame show_frame flag is false")
	}
	firstPartitionLen := int(frameTag >> 5)
	if firstPartitionLen <= 0 || 10+firstPartitionLen >= len(frame) {
		t.Fatalf("first partition length = %d, frame length = %d", firstPartitionLen, len(frame))
	}
	if !bytes.Equal(frame[3:6], []byte{0x9d, 0x01, 0x2a}) {
		t.Fatalf("invalid VP8 start code: % x", frame[3:6])
	}
	width := int(binary.LittleEndian.Uint16(frame[6:8]) & 0x3fff)
	height := int(binary.LittleEndian.Uint16(frame[8:10]) & 0x3fff)
	if width != wantWidth || height != wantHeight {
		t.Fatalf("VP8 dimensions = %dx%d, want %dx%d", width, height, wantWidth, wantHeight)
	}
}

func readVP8LoopFilterHeader(t *testing.T, frame []byte) vp8LoopFilter {
	t.Helper()
	firstPart := readVP8FirstPartition(t, frame)
	var r testVP8PartitionReader
	r.init(firstPart)

	colorSpace := r.readUint(128, 1)
	pixelClamp := r.readUint(128, 1)
	segmentation := r.readBit(128)
	simple := r.readBit(128)
	level := r.readUint(128, 6)
	sharpness := r.readUint(128, 3)
	loopFilterDelta := r.readBit(128)
	if r.unexpectedEOF {
		t.Fatal("unexpected end of VP8 first partition")
	}
	if colorSpace != 0 {
		t.Fatalf("VP8 color space = %d, want 0", colorSpace)
	}
	if pixelClamp != 0 {
		t.Fatalf("VP8 pixel clamp = %d, want 0", pixelClamp)
	}
	if segmentation {
		t.Fatal("VP8 segmentation is enabled, want disabled")
	}
	if loopFilterDelta {
		t.Fatal("VP8 loop filter delta is enabled, want disabled")
	}
	return vp8LoopFilter{
		simple:    simple,
		level:     int(level),
		sharpness: int(sharpness),
	}
}

func readVP8FirstPartition(t *testing.T, frame []byte) []byte {
	t.Helper()
	if len(frame) < 10 {
		t.Fatalf("VP8 frame length = %d, want at least 10", len(frame))
	}
	frameTag := uint32(frame[0]) | uint32(frame[1])<<8 | uint32(frame[2])<<16
	firstPartitionLen := int(frameTag >> 5)
	if firstPartitionLen <= 0 || 10+firstPartitionLen >= len(frame) {
		t.Fatalf("first partition length = %d, frame length = %d", firstPartitionLen, len(frame))
	}
	return frame[10 : 10+firstPartitionLen]
}

func readVP8FirstPartitionY4Modes(t *testing.T, firstPart []byte) [16]uint8 {
	t.Helper()
	var r testVP8PartitionReader
	r.init(firstPart)

	r.readUint(128, 1) // color space
	r.readUint(128, 1) // pixel clamp
	r.readBit(128)     // segmentation
	r.readBit(128)     // loop filter type
	r.readUint(128, 6) // loop filter level
	r.readUint(128, 3) // sharpness
	r.readBit(128)     // loop filter delta
	r.readUint(128, 2) // token partitions
	r.readUint(128, 7) // base quantizer
	for i := 0; i < 5; i++ {
		r.readBit(128)
	}
	r.readBit(128) // refresh last frame buffer
	for plane := range vp8TokenProbUpdateProb {
		for band := range vp8TokenProbUpdateProb[plane] {
			for context := range vp8TokenProbUpdateProb[plane][band] {
				for _, prob := range vp8TokenProbUpdateProb[plane][band][context] {
					r.readBit(prob)
				}
			}
		}
	}
	r.readBit(128) // macroblock skip probability
	if r.unexpectedEOF {
		t.Fatal("unexpected end before Y4 modes")
	}
	if useY16 := r.readBit(145); useY16 {
		t.Fatal("macroblock uses Y16 mode, want Y4")
	}

	var modes [16]uint8
	var up [4]uint8
	for by := 0; by < 4; by++ {
		p := uint8(0)
		for bx := 0; bx < 4; bx++ {
			mode := readVP8Y4Mode(&r, vp8PredProb[up[bx]][p])
			modes[by*4+bx] = mode
			p = mode
			up[bx] = mode
		}
	}
	if r.unexpectedEOF {
		t.Fatal("unexpected end while reading Y4 modes")
	}
	return modes
}

func readVP8Y4Mode(r *testVP8PartitionReader, prob [9]uint8) uint8 {
	if !r.readBit(prob[0]) {
		return vp8PredDC
	}
	if !r.readBit(prob[1]) {
		return vp8PredTM
	}
	if !r.readBit(prob[2]) {
		return vp8PredVE
	}
	if !r.readBit(prob[3]) {
		if !r.readBit(prob[4]) {
			return vp8PredHE
		}
		if !r.readBit(prob[5]) {
			return vp8PredRD
		}
		return vp8PredVR
	}
	if !r.readBit(prob[6]) {
		return vp8PredLD
	}
	if !r.readBit(prob[7]) {
		return vp8PredVL
	}
	if !r.readBit(prob[8]) {
		return vp8PredHD
	}
	return vp8PredHU
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
	if err := Encode(&buf, image.NewNRGBA(image.Rect(0, 0, maxVP8Dimension+1, 1)), &Options{Compression: CompressionLossy}); err == nil {
		t.Fatal("Encode lossy with too-wide image succeeded")
	}
	if err := Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 1, 1)), &Options{Compression: Compression(99)}); err == nil {
		t.Fatal("Encode with unsupported compression succeeded")
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

type testWebPChunk struct {
	name    string
	payload []byte
}

func readWebPChunks(t *testing.T, data []byte) []testWebPChunk {
	t.Helper()
	if len(data) < 12 {
		t.Fatalf("WebP length = %d, want at least 12", len(data))
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		t.Fatalf("unexpected WebP header: %q %q", data[0:4], data[8:12])
	}
	riffSize := int(binary.LittleEndian.Uint32(data[4:8]))
	if riffSize+8 != len(data) {
		t.Fatalf("RIFF size = %d, file length = %d", riffSize, len(data))
	}

	var chunks []testWebPChunk
	for offset := 12; offset < len(data); {
		if offset+8 > len(data) {
			t.Fatalf("short chunk header at offset %d", offset)
		}
		payloadSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		payloadStart := offset + 8
		payloadEnd := payloadStart + payloadSize
		if payloadEnd > len(data) {
			t.Fatalf("chunk %q payload size = %d exceeds file length %d", data[offset:offset+4], payloadSize, len(data))
		}
		chunks = append(chunks, testWebPChunk{
			name:    string(data[offset : offset+4]),
			payload: data[payloadStart:payloadEnd],
		})
		offset = payloadEnd
		if payloadSize&1 != 0 {
			if offset >= len(data) {
				t.Fatalf("missing padding byte after chunk %q", chunks[len(chunks)-1].name)
			}
			if data[offset] != 0 {
				t.Fatalf("padding byte after chunk %q = %#02x, want 0", chunks[len(chunks)-1].name, data[offset])
			}
			offset++
		}
	}
	return chunks
}

func readUint24LE(b []byte) int {
	return int(b[0]) | int(b[1])<<8 | int(b[2])<<16
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

type testVP8PartitionReader struct {
	buf           []byte
	r             int
	rangeM1       uint32
	bits          uint32
	nBits         uint8
	unexpectedEOF bool
}

func (p *testVP8PartitionReader) init(buf []byte) {
	p.buf = buf
	p.r = 0
	p.rangeM1 = 254
	p.bits = 0
	p.nBits = 0
	p.unexpectedEOF = false
}

func (p *testVP8PartitionReader) readBit(prob uint8) bool {
	if p.nBits < 8 {
		if p.r >= len(p.buf) {
			p.unexpectedEOF = true
			return false
		}
		p.bits |= uint32(p.buf[p.r]) << (8 - p.nBits)
		p.r++
		p.nBits += 8
	}

	split := (p.rangeM1*uint32(prob))>>8 + 1
	bit := p.bits >= split<<8
	if bit {
		p.rangeM1 -= split
		p.bits -= split << 8
	} else {
		p.rangeM1 = split - 1
	}
	for p.rangeM1 < 127 {
		p.rangeM1 = p.rangeM1<<1 | 1
		p.bits <<= 1
		p.nBits--
	}
	return bit
}

func (p *testVP8PartitionReader) readUint(prob uint8, n uint8) uint32 {
	var u uint32
	for n > 0 {
		n--
		if p.readBit(prob) {
			u |= 1 << n
		}
	}
	return u
}
