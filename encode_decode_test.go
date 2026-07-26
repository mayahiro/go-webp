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

func hasVP8LFirstTransform(t *testing.T, data []byte, wantType uint32) bool {
	t.Helper()
	if len(data) < 21 {
		t.Fatalf("WebP length = %d, want at least 21", len(data))
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" || string(data[12:16]) != "VP8L" {
		t.Fatalf("unexpected WebP header: %q %q %q", data[0:4], data[8:12], data[12:16])
	}
	payloadSize := int(binary.LittleEndian.Uint32(data[16:20]))
	if 20+payloadSize > len(data) {
		t.Fatalf("VP8L payload size = %d exceeds file length %d", payloadSize, len(data))
	}
	r := testBitReader{data: data[20 : 20+payloadSize]}
	if signature, err := r.read(8); err != nil || signature != 0x2f {
		t.Fatalf("invalid VP8L signature: signature=%#x err=%v", signature, err)
	}
	r.read(14)
	r.read(14)
	r.read(1)
	if version, err := r.read(3); err != nil || version != 0 {
		t.Fatalf("invalid VP8L version: version=%d err=%v", version, err)
	}
	transformPresent, err := r.read(1)
	if err != nil {
		t.Fatalf("reading transform presence failed: %v", err)
	}
	if transformPresent == 0 {
		return false
	}
	transformType, err := r.read(2)
	if err != nil {
		t.Fatalf("reading transform type failed: %v", err)
	}
	return transformType == wantType
}

type decodedTree struct {
	constant bool
	symbol   int
	lengths  []uint8
	codes    []uint16
}

type testPredictorTransform struct {
	sizeBits uint8
	width    int
	pixels   []color.NRGBA
}

type testColorTransform struct {
	sizeBits uint8
	width    int
	pixels   []color.NRGBA
}

type testColorIndexTransform struct {
	width     int
	widthBits uint8
	table     []color.NRGBA
}

type testVP8LTransformType uint8

const (
	testVP8LTransformPredictor     testVP8LTransformType = 0
	testVP8LTransformColor         testVP8LTransformType = 1
	testVP8LTransformSubtractGreen testVP8LTransformType = 2
	testVP8LTransformColorIndexing testVP8LTransformType = 3
)

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

	width, height := int(widthMinusOne+1), int(heightMinusOne+1)
	currentWidth := width
	var predictor *testPredictorTransform
	var colorTransform *testColorTransform
	var colorIndex *testColorIndexTransform
	var subtractGreen bool
	var transforms []testVP8LTransformType
	for {
		transformPresent, err := r.read(1)
		if err != nil {
			return nil, 0, 0, false, err
		}
		if transformPresent == 0 {
			break
		}
		transformType, err := r.read(2)
		if err != nil {
			return nil, 0, 0, false, err
		}
		switch testVP8LTransformType(transformType) {
		case testVP8LTransformPredictor:
			if predictor != nil {
				return nil, 0, 0, false, errors.New("duplicate predictor transform")
			}
			sizeBitsMinusTwo, err := r.read(3)
			if err != nil {
				return nil, 0, 0, false, err
			}
			sizeBits := uint8(sizeBitsMinusTwo + 2)
			transformWidth, transformHeight := vp8lTransformDimensions(currentWidth, height, sizeBits)
			transformPixels, err := decodeEncoderImageData(&r, transformWidth, transformHeight, false)
			if err != nil {
				return nil, 0, 0, false, err
			}
			predictor = &testPredictorTransform{
				sizeBits: sizeBits,
				width:    transformWidth,
				pixels:   transformPixels,
			}
			transforms = append(transforms, testVP8LTransformPredictor)
		case testVP8LTransformColor:
			if colorTransform != nil {
				return nil, 0, 0, false, errors.New("duplicate color transform")
			}
			sizeBitsMinusTwo, err := r.read(3)
			if err != nil {
				return nil, 0, 0, false, err
			}
			sizeBits := uint8(sizeBitsMinusTwo + 2)
			transformWidth, transformHeight := vp8lTransformDimensions(currentWidth, height, sizeBits)
			transformPixels, err := decodeEncoderImageData(&r, transformWidth, transformHeight, false)
			if err != nil {
				return nil, 0, 0, false, err
			}
			colorTransform = &testColorTransform{
				sizeBits: sizeBits,
				width:    transformWidth,
				pixels:   transformPixels,
			}
			transforms = append(transforms, testVP8LTransformColor)
		case testVP8LTransformSubtractGreen:
			if subtractGreen {
				return nil, 0, 0, false, errors.New("duplicate subtract green transform")
			}
			subtractGreen = true
			transforms = append(transforms, testVP8LTransformSubtractGreen)
		case testVP8LTransformColorIndexing:
			if colorIndex != nil {
				return nil, 0, 0, false, errors.New("duplicate color indexing transform")
			}
			colorTableSizeMinusOne, err := r.read(8)
			if err != nil {
				return nil, 0, 0, false, err
			}
			colorTableSize := int(colorTableSizeMinusOne) + 1
			tableDeltas, err := decodeEncoderImageData(&r, colorTableSize, 1, false)
			if err != nil {
				return nil, 0, 0, false, err
			}
			table := make([]color.NRGBA, colorTableSize)
			for i, delta := range tableDeltas {
				if i == 0 {
					table[i] = delta
				} else {
					table[i] = addNRGBA(table[i-1], delta)
				}
			}
			widthBits := vp8lColorIndexWidthBits(colorTableSize)
			colorIndex = &testColorIndexTransform{
				width:     currentWidth,
				widthBits: widthBits,
				table:     table,
			}
			currentWidth = vp8lDivRoundUp(currentWidth, 1<<widthBits)
			transforms = append(transforms, testVP8LTransformColorIndexing)
		default:
			return nil, 0, 0, false, errors.New("unexpected transform")
		}
	}

	pixels, err := decodeEncoderImageData(&r, currentWidth, height, true)
	if err != nil {
		return nil, 0, 0, false, err
	}
	imageWidth := currentWidth
	for i := len(transforms) - 1; i >= 0; i-- {
		switch transforms[i] {
		case testVP8LTransformPredictor:
			pixels = applyTestPredictorTransform(pixels, imageWidth, height, *predictor)
		case testVP8LTransformColor:
			applyTestColorTransform(pixels, imageWidth, height, *colorTransform)
		case testVP8LTransformSubtractGreen:
			applyTestSubtractGreenTransform(pixels)
		case testVP8LTransformColorIndexing:
			pixels = applyTestColorIndexTransform(pixels, imageWidth, height, *colorIndex)
			imageWidth = colorIndex.width
		}
	}

	return pixels, imageWidth, height, alphaHint != 0, nil
}

type decodedPrefixGroup struct {
	green    decodedTree
	red      decodedTree
	blue     decodedTree
	alpha    decodedTree
	distance decodedTree
}

func decodeEncoderImageData(r *testBitReader, width int, height int, metaPrefix bool) ([]color.NRGBA, error) {
	colorCacheBits := uint8(0)
	colorCacheSize := 0
	if v, err := r.read(1); err != nil {
		return nil, err
	} else if v != 0 {
		bits, err := r.read(4)
		if err != nil {
			return nil, err
		}
		if bits < 1 || bits > 11 {
			return nil, errors.New("invalid color cache bits")
		}
		colorCacheBits = uint8(bits)
		colorCacheSize = 1 << colorCacheBits
	}
	prefixBits := uint8(0)
	prefixImageWidth := 0
	var entropyImage []color.NRGBA
	groupCount := 1
	if metaPrefix {
		v, err := r.read(1)
		if err != nil {
			return nil, err
		}
		if v != 0 {
			rawPrefixBits, err := r.read(3)
			if err != nil {
				return nil, err
			}
			prefixBits = uint8(rawPrefixBits) + 2
			var prefixImageHeight int
			prefixImageWidth, prefixImageHeight = testVP8LMetaPrefixImageDimensions(width, height, prefixBits)
			entropyImage, err = decodeEncoderImageData(r, prefixImageWidth, prefixImageHeight, false)
			if err != nil {
				return nil, err
			}
			maxCode := 0
			for _, pixel := range entropyImage {
				if code := testVP8LMetaPrefixCode(pixel); code > maxCode {
					maxCode = code
				}
			}
			groupCount = maxCode + 1
		}
	}

	groups := make([]decodedPrefixGroup, groupCount)
	for i := range groups {
		green, err := decodeEncoderTree(r, nLiteralCodes+nLengthCodes+colorCacheSize)
		if err != nil {
			return nil, err
		}
		red, err := decodeEncoderTree(r, nLiteralCodes)
		if err != nil {
			return nil, err
		}
		blue, err := decodeEncoderTree(r, nLiteralCodes)
		if err != nil {
			return nil, err
		}
		alpha, err := decodeEncoderTree(r, nLiteralCodes)
		if err != nil {
			return nil, err
		}
		distance, err := decodeEncoderTree(r, nDistanceCodes)
		if err != nil {
			return nil, err
		}
		groups[i] = decodedPrefixGroup{
			green:    green,
			red:      red,
			blue:     blue,
			alpha:    alpha,
			distance: distance,
		}
	}

	pixels := make([]color.NRGBA, width*height)
	colorCache := make([]color.NRGBA, colorCacheSize)
	for i := 0; i < len(pixels); {
		group := groups[0]
		groupIndex := 0
		if entropyImage != nil {
			x := i % width
			y := i / width
			code := testVP8LMetaPrefixCode(entropyImage[testVP8LMetaPrefixIndex(x, y, prefixBits, prefixImageWidth)])
			if code < 0 || code >= len(groups) {
				return nil, errors.New("meta prefix code out of range")
			}
			groupIndex = code
			group = groups[code]
		}
		greenSymbol, err := decodeEncoderSymbolInt(r, group.green)
		if err != nil {
			return nil, fmt.Errorf("green symbol at pixel %d group %d: %w", i, groupIndex, err)
		}
		if greenSymbol >= nLiteralCodes+nLengthCodes {
			index := greenSymbol - nLiteralCodes - nLengthCodes
			if index < 0 || index >= len(colorCache) {
				return nil, fmt.Errorf("color cache index %d at pixel %d group %d out of range", index, i, groupIndex)
			}
			pixel := colorCache[index]
			pixels[i] = pixel
			updateTestVP8LColorCache(colorCache, colorCacheBits, pixel)
			i++
			continue
		}
		if greenSymbol >= nLiteralCodes && greenSymbol < nLiteralCodes+nLengthCodes {
			length, err := decodeVP8LPrefixValue(r, greenSymbol-nLiteralCodes)
			if err != nil {
				return nil, fmt.Errorf("length prefix %d at pixel %d group %d: %w", greenSymbol-nLiteralCodes, i, groupIndex, err)
			}
			distancePrefix, err := decodeEncoderSymbolInt(r, group.distance)
			if err != nil {
				return nil, fmt.Errorf("distance symbol at pixel %d group %d length %d: %w", i, groupIndex, length, err)
			}
			distanceCode, err := decodeVP8LPrefixValue(r, distancePrefix)
			if err != nil {
				return nil, fmt.Errorf("distance prefix %d at pixel %d group %d length %d: %w", distancePrefix, i, groupIndex, length, err)
			}
			distancePixels, err := testVP8LDistanceCodeToDistance(distanceCode, width)
			if err != nil {
				return nil, fmt.Errorf("distance code %d at pixel %d group %d length %d: %w", distanceCode, i, groupIndex, length, err)
			}
			if distancePixels > i {
				return nil, fmt.Errorf("backward reference before image start at pixel %d group %d length %d distancePrefix %d distanceCode %d distancePixels %d", i, groupIndex, length, distancePrefix, distanceCode, distancePixels)
			}
			if i+length > len(pixels) {
				return nil, fmt.Errorf("backward reference exceeds image at pixel %d group %d length %d distancePrefix %d distanceCode %d distancePixels %d total %d", i, groupIndex, length, distancePrefix, distanceCode, distancePixels, len(pixels))
			}
			for copied := 0; copied < length; copied++ {
				pixel := pixels[i-distancePixels]
				pixels[i] = pixel
				updateTestVP8LColorCache(colorCache, colorCacheBits, pixel)
				i++
			}
			continue
		}
		rr, err := decodeEncoderSymbol(r, group.red)
		if err != nil {
			return nil, fmt.Errorf("red symbol at pixel %d group %d green %d: %w", i, groupIndex, greenSymbol, err)
		}
		b, err := decodeEncoderSymbol(r, group.blue)
		if err != nil {
			return nil, fmt.Errorf("blue symbol at pixel %d group %d green %d: %w", i, groupIndex, greenSymbol, err)
		}
		a, err := decodeEncoderSymbol(r, group.alpha)
		if err != nil {
			return nil, fmt.Errorf("alpha symbol at pixel %d group %d green %d: %w", i, groupIndex, greenSymbol, err)
		}
		pixel := color.NRGBA{R: rr, G: uint8(greenSymbol), B: b, A: a}
		pixels[i] = pixel
		updateTestVP8LColorCache(colorCache, colorCacheBits, pixel)
		i++
	}

	return pixels, nil
}

func updateTestVP8LColorCache(cache []color.NRGBA, bits uint8, pixel color.NRGBA) {
	if len(cache) == 0 {
		return
	}
	cache[testVP8LColorCacheIndex(pixel, bits)] = pixel
}

func applyTestPredictorTransform(residual []color.NRGBA, width int, height int, transform testPredictorTransform) []color.NRGBA {
	pixels := make([]color.NRGBA, len(residual))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := y*width + x
			mode := transform.pixels[(y>>transform.sizeBits)*transform.width+(x>>transform.sizeBits)].G
			pred := testPredictorPixel(pixels, width, x, y, mode)
			pixels[i] = addNRGBA(residual[i], pred)
		}
	}
	return pixels
}

func applyTestSubtractGreenTransform(pixels []color.NRGBA) {
	for i := range pixels {
		pixels[i].R += pixels[i].G
		pixels[i].B += pixels[i].G
	}
}

func applyTestColorTransform(pixels []color.NRGBA, width int, height int, transform testColorTransform) {
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			i := y*width + x
			c := transform.pixels[(y>>transform.sizeBits)*transform.width+(x>>transform.sizeBits)]
			element := vp8lColorTransformElement{
				greenToRed:  c.B,
				greenToBlue: c.G,
				redToBlue:   c.R,
			}
			pixels[i] = inverseTestVP8LColorTransform(pixels[i], element)
		}
	}
}

func testVP8LMetaPrefixImageDimensions(width int, height int, prefixBits uint8) (int, int) {
	blockSize := 1 << prefixBits
	return vp8lDivRoundUp(width, blockSize), vp8lDivRoundUp(height, blockSize)
}

func testVP8LMetaPrefixIndex(x int, y int, prefixBits uint8, prefixImageWidth int) int {
	return (y>>prefixBits)*prefixImageWidth + (x >> prefixBits)
}

func testVP8LMetaPrefixCode(pixel color.NRGBA) int {
	return int(pixel.R)<<8 | int(pixel.G)
}

func testVP8LColorCacheIndex(pixel color.NRGBA, bits uint8) int {
	packed := uint32(pixel.A)<<24 | uint32(pixel.R)<<16 | uint32(pixel.G)<<8 | uint32(pixel.B)
	return int((0x1e35a7bd * packed) >> (32 - bits))
}

func inverseTestVP8LColorTransform(pixel color.NRGBA, element vp8lColorTransformElement) color.NRGBA {
	red := pixel.R + vp8lColorDelta(element.greenToRed, pixel.G)
	blue := pixel.B + vp8lColorDelta(element.greenToBlue, pixel.G) + vp8lColorDelta(element.redToBlue, red)
	return color.NRGBA{R: red, G: pixel.G, B: blue, A: pixel.A}
}

func applyTestColorIndexTransform(indexed []color.NRGBA, indexedWidth int, height int, transform testColorIndexTransform) []color.NRGBA {
	pixels := make([]color.NRGBA, transform.width*height)
	groupSize := 1 << transform.widthBits
	bitsPerIndex := 8 / groupSize
	mask := uint8((1 << bitsPerIndex) - 1)
	for y := 0; y < height; y++ {
		for x := 0; x < transform.width; x++ {
			packed := indexed[y*indexedWidth+(x>>transform.widthBits)].G
			index := (packed >> uint((x&(groupSize-1))*bitsPerIndex)) & mask
			if int(index) < len(transform.table) {
				pixels[y*transform.width+x] = transform.table[index]
			}
		}
	}
	return pixels
}

func testPredictorPixel(pixels []color.NRGBA, width int, x int, y int, mode uint8) color.NRGBA {
	if x == 0 && y == 0 {
		return color.NRGBA{A: 255}
	}
	if y == 0 {
		return pixels[y*width+x-1]
	}
	if x == 0 {
		return pixels[(y-1)*width+x]
	}

	left := pixels[y*width+x-1]
	top := pixels[(y-1)*width+x]
	topLeft := pixels[(y-1)*width+x-1]
	topRightX := x + 1
	topRightY := y - 1
	if x == width-1 {
		topRightX = 0
		topRightY = y
	}
	topRight := pixels[topRightY*width+topRightX]
	return vp8lPredictorFromNeighbors(mode, left, top, topRight, topLeft)
}

func addNRGBA(a color.NRGBA, b color.NRGBA) color.NRGBA {
	return color.NRGBA{
		R: a.R + b.R,
		G: a.G + b.G,
		B: a.B + b.B,
		A: a.A + b.A,
	}
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
		if nSymbols == 0 {
			return decodedTree{constant: true, symbol: int(symbol)}, nil
		}
		symbol1, err := r.read(8)
		if err != nil {
			return decodedTree{}, err
		}
		if int(symbol1) >= alphabetSize {
			return decodedTree{}, errors.New("simple tree symbol out of range")
		}
		lengths := make([]uint8, alphabetSize)
		lengths[symbol] = 1
		lengths[symbol1] = 1
		return decodedTree{lengths: lengths, codes: testCanonicalCodes(lengths)}, nil
	}

	nCodesMinusFour, err := r.read(4)
	if err != nil {
		return decodedTree{}, err
	}
	nCodes := int(nCodesMinusFour) + 4
	if nCodes > len(normalCodeLengthCodeOrder) {
		return decodedTree{}, errors.New("unexpected code length code count")
	}
	codeLengthCodeLengths := make([]uint8, alphaCodeLengthCodeCount)
	for i := 0; i < nCodes; i++ {
		got, err := r.read(3)
		if err != nil {
			return decodedTree{}, err
		}
		codeLengthCodeLengths[normalCodeLengthCodeOrder[i]] = uint8(got)
	}
	useLength, err := r.read(1)
	if err != nil {
		return decodedTree{}, err
	}
	maxSymbol := alphabetSize
	if useLength != 0 {
		lengthNBitsSelector, err := r.read(3)
		if err != nil {
			return decodedTree{}, err
		}
		lengthNBits := uint8(2 + 2*lengthNBitsSelector)
		value, err := r.read(lengthNBits)
		if err != nil {
			return decodedTree{}, err
		}
		maxSymbol = int(value) + 2
		if maxSymbol > alphabetSize {
			return decodedTree{}, errors.New("max symbol limit out of range")
		}
	}

	codeLengthTree := decodedTree{
		lengths: codeLengthCodeLengths,
		codes:   testCanonicalCodes(codeLengthCodeLengths),
	}
	lengths := make([]uint8, alphabetSize)
	previousNonZero := uint8(8)
	for symbol, remainingTokens := 0, maxSymbol; symbol < alphabetSize && remainingTokens > 0; remainingTokens-- {
		codeLengthSymbol, err := decodeEncoderSymbolInt(r, codeLengthTree)
		if err != nil {
			return decodedTree{}, err
		}
		switch {
		case codeLengthSymbol >= 0 && codeLengthSymbol <= 15:
			lengths[symbol] = uint8(codeLengthSymbol)
			if codeLengthSymbol != 0 {
				previousNonZero = uint8(codeLengthSymbol)
			}
			symbol++
		case codeLengthSymbol == 16:
			repeatExtra, err := r.read(2)
			if err != nil {
				return decodedTree{}, err
			}
			repeat := int(repeatExtra) + 3
			for range repeat {
				if symbol >= alphabetSize {
					return decodedTree{}, errors.New("code length repeat exceeds max symbol")
				}
				lengths[symbol] = previousNonZero
				symbol++
			}
		case codeLengthSymbol == alphaCodeLengthRepeatZero:
			repeatExtra, err := r.read(3)
			if err != nil {
				return decodedTree{}, err
			}
			symbol += int(repeatExtra) + 3
			if symbol > alphabetSize {
				return decodedTree{}, errors.New("zero repeat exceeds max symbol")
			}
		case codeLengthSymbol == alphaCodeLengthRepeatZeroBig:
			repeatExtra, err := r.read(7)
			if err != nil {
				return decodedTree{}, err
			}
			symbol += int(repeatExtra) + 11
			if symbol > alphabetSize {
				return decodedTree{}, errors.New("zero repeat exceeds max symbol")
			}
		default:
			return decodedTree{}, errors.New("unexpected code length symbol")
		}
	}
	return decodedTree{lengths: lengths, codes: testCanonicalCodes(lengths)}, nil
}

func decodeEncoderSymbol(r *testBitReader, tree decodedTree) (uint8, error) {
	symbol, err := decodeEncoderSymbolInt(r, tree)
	if err != nil {
		return 0, err
	}
	if symbol > 255 {
		return 0, errors.New("symbol out of uint8 range")
	}
	return uint8(symbol), nil
}

func decodeEncoderSymbolInt(r *testBitReader, tree decodedTree) (int, error) {
	if tree.constant {
		return tree.symbol, nil
	}
	var code uint32
	for length := uint8(1); length <= 15; length++ {
		bit, err := r.read(1)
		if err != nil {
			return 0, err
		}
		code |= bit << (length - 1)
		for symbol, symbolLength := range tree.lengths {
			if symbolLength != length {
				continue
			}
			if code == uint32(reverseBits(tree.codes[symbol], length)) {
				return symbol, nil
			}
		}
	}
	return 0, errors.New("invalid Huffman symbol")
}

func testCanonicalCodes(lengths []uint8) []uint16 {
	var histogram [16]uint16
	for _, length := range lengths {
		if length != 0 {
			histogram[length]++
		}
	}

	code := uint16(0)
	var nextCodes [16]uint16
	for length := 1; length < len(nextCodes); length++ {
		code = (code + histogram[length-1]) << 1
		nextCodes[length] = code
	}

	codes := make([]uint16, len(lengths))
	for symbol, length := range lengths {
		if length == 0 {
			continue
		}
		codes[symbol] = nextCodes[length]
		nextCodes[length]++
	}
	return codes
}

func decodeVP8LPrefixValue(r *testBitReader, prefixCode int) (int, error) {
	if prefixCode < 0 || prefixCode >= nDistanceCodes {
		return 0, errors.New("prefix code out of range")
	}
	if prefixCode < 4 {
		return prefixCode + 1, nil
	}
	extraBits := uint8((prefixCode - 2) >> 1)
	extra, err := r.read(extraBits)
	if err != nil {
		return 0, err
	}
	offset := (2 + prefixCode&1) << extraBits
	return offset + int(extra) + 1, nil
}

func testVP8LDistanceCodeToDistance(distanceCode int, width int) (int, error) {
	if distanceCode > 120 {
		return distanceCode - 120, nil
	}
	if distanceCode < 1 || distanceCode > len(vp8lDistanceMap) {
		return 0, errors.New("unsupported VP8L distance code")
	}
	offset := vp8lDistanceMap[distanceCode-1]
	distance := offset.x + offset.y*width
	if distance < 1 {
		distance = 1
	}
	return distance, nil
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
