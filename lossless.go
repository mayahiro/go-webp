package webp

import (
	"bufio"
	"fmt"
	"image"
	"io"
	"math"
)

func encodeLossless(w io.Writer, m image.Image, bounds image.Rectangle, width int, height int, mode Mode) error {
	if width > maxVP8LDimension || height > maxVP8LDimension {
		return fmt.Errorf("webp: image dimensions %dx%d exceed VP8L limit %dx%d", width, height, maxVP8LDimension, maxVP8LDimension)
	}

	readPixel := pixelReaderFor(m)
	plan := chooseVP8LEncodingPlanForImageMode(m, readPixel, bounds, width, height, mode)
	return writeLosslessVP8L(w, readPixel, bounds, width, height, plan)
}

func encodeNearLossless(w io.Writer, m image.Image, bounds image.Rectangle, width int, height int, quality int, mode Mode) error {
	if width > maxVP8LDimension || height > maxVP8LDimension {
		return fmt.Errorf("webp: image dimensions %dx%d exceed VP8L limit %dx%d", width, height, maxVP8LDimension, maxVP8LDimension)
	}

	step := nearLosslessQuantizationStep(quality)
	if step <= 1 {
		return encodeLossless(w, m, bounds, width, height, mode)
	}
	readPixel := nearLosslessReader(pixelReaderFor(m), step)
	plan := chooseVP8LEncodingPlanForImageMode(nil, readPixel, bounds, width, height, mode)
	return writeLosslessVP8L(w, readPixel, bounds, width, height, plan)
}

func writeLosslessVP8L(w io.Writer, readPixel pixelReader, bounds image.Rectangle, width int, height int, plan vp8lEncodingPlan) error {
	payloadBits := vp8lPayloadBits(width, height, plan)
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
	writeVP8L(bits, readPixel, bounds, width, height, plan)
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
