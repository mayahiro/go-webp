package webp

import (
	"bufio"
	"fmt"
	"io"
	"math"
)

func encodeLossless(w io.Writer, source encoderSource, mode Mode) error {
	if source.width > maxVP8LDimension || source.height > maxVP8LDimension {
		return fmt.Errorf("webp: image dimensions %dx%d exceed VP8L limit %dx%d", source.width, source.height, maxVP8LDimension, maxVP8LDimension)
	}

	readPixel := source.pixels()
	plan := chooseVP8LEncodingPlanForImageMode(source.image, readPixel, source.bounds, source.width, source.height, mode)
	return writeLosslessVP8L(w, source, readPixel, plan)
}

func encodeNearLossless(w io.Writer, source encoderSource, quality int, mode Mode) error {
	if source.width > maxVP8LDimension || source.height > maxVP8LDimension {
		return fmt.Errorf("webp: image dimensions %dx%d exceed VP8L limit %dx%d", source.width, source.height, maxVP8LDimension, maxVP8LDimension)
	}

	if nearLosslessQuantizationBits(quality) == 0 {
		return encodeLossless(w, source, ModeDefault)
	}
	readPixel := newNearLosslessReader(source, quality)
	plan := chooseVP8LEncodingPlanForImageMode(nil, readPixel, source.bounds, source.width, source.height, mode)
	return writeLosslessVP8L(w, source, readPixel, plan)
}

func writeLosslessVP8L(w io.Writer, source encoderSource, readPixel pixelReader, plan vp8lEncodingPlan) error {
	payloadBits := vp8lPayloadBits(source.width, source.height, plan)
	payloadSize := (payloadBits + 7) / 8
	padding := payloadSize & 1
	riffSize := uint64(4) + 8 + payloadSize + padding
	if riffSize > math.MaxUint32 {
		return fmt.Errorf("webp: encoded image is too large")
	}

	bw := bufio.NewWriter(w)
	if err := writeWebPHeader(bw, "VP8L", uint32(riffSize), uint32(payloadSize)); err != nil {
		return err
	}

	bits := newBitWriter(bw)
	writeVP8L(bits, readPixel, source.bounds, source.width, source.height, plan)
	if err := bits.flush(); err != nil {
		return err
	}
	if padding != 0 {
		if err := bw.WriteByte(0); err != nil {
			return err
		}
	}
	return bw.Flush()
}
