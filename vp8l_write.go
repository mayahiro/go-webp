package webp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
)

var vp8lFull8CodeLengthCodeLengths = [...]uint8{
	0, // 17
	0, // 18
	1, // 0
	0, // 1
	0, // 2
	0, // 3
	0, // 4
	0, // 5
	0, // 16
	0, // 6
	0, // 7
	1, // 8
}

type vp8lEncodedPlan interface {
	payloadBitLen() uint64
	writeTo(*vp8lBitSink)
}

func encodeLossless(w io.Writer, source encoderSource, mode Mode) error {
	if source.width > maxVP8LDimension || source.height > maxVP8LDimension {
		return fmt.Errorf("webp: image dimensions %dx%d exceed VP8L limit %dx%d", source.width, source.height, maxVP8LDimension, maxVP8LDimension)
	}
	readPixel := source.pixels()
	if mode == ModeAuto {
		mode = vp8lAutoLosslessMode(source.image, readPixel, source.bounds, source.width, source.height)
	}
	vp8lSource := newVP8LSource(source, readPixel)
	plan, err := vp8lPlanForMode(vp8lSource, mode)
	if err != nil {
		return err
	}
	return writeLosslessVP8L(w, plan)
}

func vp8lPlanForMode(source vp8lSource, mode Mode) (vp8lEncodedPlan, error) {
	if mode == ModeFast || mode == ModeLowMemory {
		return searchVP8LStreaming(source, mode)
	}
	if mode != ModeBestCompression {
		return vp8lBufferedPlanOrStreaming(source, mode)
	}
	return vp8lBestPlanOrStreaming(source)
}

func vp8lBufferedPlanOrStreaming(source vp8lSource, mode Mode) (vp8lEncodedPlan, error) {
	plan, err := searchVP8L(source, vp8lBudgetForMode(mode))
	if errors.Is(err, errVP8LSourceLimit) {
		return searchVP8LStreaming(source, mode)
	}
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func encodeNearLossless(w io.Writer, source encoderSource, quality int) error {
	if source.width > maxVP8LDimension || source.height > maxVP8LDimension {
		return fmt.Errorf("webp: image dimensions %dx%d exceed VP8L limit %dx%d", source.width, source.height, maxVP8LDimension, maxVP8LDimension)
	}
	if nearLosslessQuantizationBits(quality) == 0 {
		return encodeLossless(w, source, ModeDefault)
	}
	readPixel := newNearLosslessReader(source, quality)
	plan, err := searchVP8LStreaming(newVP8LSource(source, readPixel), ModeNearLossless)
	if err != nil {
		return err
	}
	return writeLosslessVP8L(w, plan)
}

func writeLosslessVP8L(w io.Writer, plan vp8lEncodedPlan) error {
	payloadSize := (plan.payloadBitLen() + 7) / 8
	padding := payloadSize & 1
	riffSize := uint64(4) + 8 + payloadSize + padding
	if riffSize > math.MaxUint32 || payloadSize > math.MaxUint32 {
		return fmt.Errorf("webp: encoded image is too large")
	}

	buffered := bufio.NewWriter(w)
	if err := writeWebPHeader(buffered, "VP8L", uint32(riffSize), uint32(payloadSize)); err != nil {
		return err
	}
	bits := vp8lBitWriter(buffered)
	plan.writeTo(bits)
	if bits.bitLen != plan.payloadBitLen() {
		return fmt.Errorf("webp: VP8L plan size changed during emission: got %d bits, want %d", bits.bitLen, plan.payloadBitLen())
	}
	if err := bits.flush(); err != nil {
		return err
	}
	if padding != 0 {
		if err := buffered.WriteByte(0); err != nil {
			return err
		}
	}
	return buffered.Flush()
}

func (p *vp8lPlan) writeTo(bits *vp8lBitSink) {
	p.writePrefixTo(bits)
	p.image.writeTo(bits, true)
}

func (p *vp8lPlan) writePrefixTo(bits *vp8lBitSink) {
	writeVP8LPrefix(bits, p.width, p.height, p.alpha, p.transforms)
}

func writeVP8LPrefix(bits *vp8lBitSink, width int, height int, alpha bool, transforms []vp8lTransform) {
	bits.writeBits(0x2f, 8)
	bits.writeBits(uint32(width-1), 14)
	bits.writeBits(uint32(height-1), 14)
	if alpha {
		bits.writeBits(1, 1)
	} else {
		bits.writeBits(0, 1)
	}
	bits.writeBits(0, 3)
	for i := range transforms {
		transforms[i].writeTo(bits)
	}
	bits.writeBits(0, 1) // no more transforms
}

func (image *vp8lImagePlan) writeTo(bits *vp8lBitSink, allowMetaPrefix bool) {
	if image.cacheBits != 0 {
		bits.writeBits(1, 1)
		bits.writeBits(uint32(image.cacheBits), 4)
	} else {
		bits.writeBits(0, 1)
	}
	if allowMetaPrefix {
		if image.meta != nil {
			bits.writeBits(1, 1)
			bits.writeBits(uint32(image.meta.prefixBits-2), 3)
			image.meta.image.writeTo(bits, false)
		} else {
			bits.writeBits(0, 1)
		}
	}
	if image.meta == nil {
		image.group.writeHeaders(bits)
	} else {
		for i := range image.meta.groups {
			image.meta.groups[i].writeHeaders(bits)
		}
	}

	position := 0
	for _, token := range image.tokens {
		group := image.codeGroupAt(position)
		switch token.kind() {
		case vp8lTokenLiteral:
			pixel := token.literal()
			group.green.writeSymbol(bits, int(uint8(pixel>>8)))
			group.red.writeSymbol(bits, int(uint8(pixel>>16)))
			group.blue.writeSymbol(bits, int(uint8(pixel)))
			group.alpha.writeSymbol(bits, int(uint8(pixel>>24)))
			position++
		case vp8lTokenCopy:
			lengthPrefix := vp8lPrefixCode(token.copyLength())
			distancePrefix := vp8lDistancePrefixCode(token.distanceCode())
			group.green.writeSymbol(bits, nLiteralCodes+lengthPrefix.code)
			bits.writeBits(lengthPrefix.extra, lengthPrefix.extraBits)
			group.distance.writeSymbol(bits, distancePrefix.code)
			bits.writeBits(distancePrefix.extra, distancePrefix.extraBits)
			position += token.copyLength()
		case vp8lTokenCache:
			group.green.writeSymbol(bits, nLiteralCodes+nLengthCodes+token.cacheIndex())
			position++
		default:
			bits.err = fmt.Errorf("webp: unsupported VP8L token kind %d", token.kind())
			return
		}
	}
}

func (group *vp8lCodeGroup) writeHeaders(bits *vp8lBitSink) {
	group.green.writeHeader(bits)
	group.red.writeHeader(bits)
	group.blue.writeHeader(bits)
	group.alpha.writeHeader(bits)
	group.distance.writeHeader(bits)
}

func (image *vp8lImagePlan) codeGroupAt(position int) *vp8lCodeGroup {
	if image.meta == nil {
		return &image.group
	}
	x := position % image.width
	y := position / image.width
	index := (y>>image.meta.prefixBits)*image.meta.width + (x >> image.meta.prefixBits)
	return &image.meta.groups[image.meta.groupMap[index]]
}

func writeVP8LSimpleTree(bits *vp8lBitSink, symbol uint8) {
	bits.writeBits(1, 1)
	bits.writeBits(0, 1)
	if symbol < 2 {
		bits.writeBits(0, 1)
		bits.writeBits(uint32(symbol), 1)
		return
	}
	bits.writeBits(1, 1)
	bits.writeBits(uint32(symbol), 8)
}

func writeVP8LFull8Tree(bits *vp8lBitSink, alphabetSize int) {
	bits.writeBits(0, 1)
	bits.writeBits(8, 4)
	for _, length := range vp8lFull8CodeLengthCodeLengths {
		bits.writeBits(uint32(length), 3)
	}
	bits.writeBits(0, 1)
	for symbol := range alphabetSize {
		if symbol < nLiteralCodes {
			bits.writeBits(1, 1)
		} else {
			bits.writeBits(0, 1)
		}
	}
}
