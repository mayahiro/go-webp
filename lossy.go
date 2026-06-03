package webp

import (
	"bufio"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
)

const (
	vp8BaseQuantizerDC   = 4
	vp8FirstPartitionMax = 1<<19 - 1
)

func encodeLossy(w io.Writer, m image.Image, bounds image.Rectangle, width int, height int) error {
	if width > maxVP8Dimension || height > maxVP8Dimension {
		return fmt.Errorf("webp: image dimensions %dx%d exceed VP8 limit %dx%d", width, height, maxVP8Dimension, maxVP8Dimension)
	}

	frame, err := encodeVP8KeyFrame(pixelReaderFor(m), bounds, width, height)
	if err != nil {
		return err
	}
	payloadSize := uint64(len(frame))
	padding := payloadSize & 1
	riffSize := uint64(4) + 8 + payloadSize + padding
	if riffSize > math.MaxUint32 {
		return fmt.Errorf("webp: encoded image is too large")
	}

	bw := bufio.NewWriter(w)
	if err := writeWebPHeader(bw, "VP8 ", uint32(riffSize), uint32(payloadSize)); err != nil {
		return err
	}
	if _, err := bw.Write(frame); err != nil {
		return err
	}
	if padding != 0 {
		if err := bw.WriteByte(0); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func encodeVP8KeyFrame(readPixel pixelReader, bounds image.Rectangle, width int, height int) ([]byte, error) {
	mbw := (width + 15) >> 4
	mbh := (height + 15) >> 4
	firstPart, err := vp8FirstPartition(mbw, mbh)
	if err != nil {
		return nil, err
	}
	residualPart := encodeVP8Residuals(readPixel, bounds, width, height, mbw, mbh)
	frameLen := 10 + len(firstPart) + len(residualPart)
	frame := make([]byte, 0, frameLen)

	tag := uint32(len(firstPart))<<5 | 1<<4
	frame = append(frame, byte(tag), byte(tag>>8), byte(tag>>16))
	frame = append(frame, 0x9d, 0x01, 0x2a)
	frame = append(frame, byte(width), byte(width>>8), byte(height), byte(height>>8))
	frame = append(frame, firstPart...)
	frame = append(frame, residualPart...)
	return frame, nil
}

func vp8FirstPartition(mbw int, mbh int) ([]byte, error) {
	bitCount := 2 + 1 + 11 + 2 + 12 + 1 + 4*8*3*11 + 1 + mbw*mbh*18
	size := (bitCount+7)/8 + 4
	if size > vp8FirstPartitionMax {
		return nil, fmt.Errorf("webp: lossy image is too large for the simple VP8 first partition")
	}
	return make([]byte, size), nil
}

func encodeVP8Residuals(readPixel pixelReader, bounds image.Rectangle, width int, height int, mbw int, mbh int) []byte {
	yStride := mbw * 16
	cStride := mbw * 8
	recY := make([]uint8, yStride*mbh*16)
	recCb := make([]uint8, cStride*mbh*8)
	recCr := make([]uint8, cStride*mbh*8)

	enc := newVP8BoolEncoder()
	upY := make([][4]uint8, mbw)
	upUV := make([][4]uint8, mbw)

	for mby := 0; mby < mbh; mby++ {
		var leftY [4]uint8
		var leftUV [4]uint8
		for mbx := 0; mbx < mbw; mbx++ {
			encodeVP8LumaMB(enc, readPixel, bounds, mbx, mby, recY, yStride, &leftY, &upY[mbx])
			encodeVP8ChromaMB(enc, readPixel, bounds, mbx, mby, recCb, recCr, cStride, &leftUV, &upUV[mbx])
		}
	}
	return enc.bytes()
}

func encodeVP8LumaMB(enc *vp8BoolEncoder, readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recY []uint8, stride int, left *[4]uint8, up *[4]uint8) {
	for by := 0; by < 4; by++ {
		nz := left[by]
		for bx := 0; bx < 4; bx++ {
			x := mbx*16 + bx*4
			y := mby*16 + by*4
			pred := pred4DC(recY, stride, x, y)
			target := averageY(readPixel, bounds, x, y)
			coeff := quantizeDC(int(target)-int(pred), vp8BaseQuantizerDC)
			blockNZ := encodeVP8Coeff(enc, vp8PlaneY1SansY2, nz+up[bx], coeff)
			recon := uint8(clipInt(int(pred)+dcDelta(coeff, vp8BaseQuantizerDC), 0, 255))
			fill4(recY, stride, x, y, recon)
			nz = blockNZ
			up[bx] = blockNZ
		}
		left[by] = nz
	}
}

func encodeVP8ChromaMB(enc *vp8BoolEncoder, readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, recCb []uint8, recCr []uint8, stride int, left *[4]uint8, up *[4]uint8) {
	encodeVP8ChromaPlane(enc, readPixel, bounds, mbx, mby, recCb, stride, left, up, true)
	encodeVP8ChromaPlane(enc, readPixel, bounds, mbx, mby, recCr, stride, left, up, false)
}

func encodeVP8ChromaPlane(enc *vp8BoolEncoder, readPixel pixelReader, bounds image.Rectangle, mbx int, mby int, rec []uint8, stride int, left *[4]uint8, up *[4]uint8, cb bool) {
	base := 0
	if !cb {
		base = 2
	}
	for by := 0; by < 2; by++ {
		nz := left[base+by]
		for bx := 0; bx < 2; bx++ {
			x := mbx*8 + bx*4
			y := mby*8 + by*4
			pred := pred8DC(rec, stride, mbx, mby, x, y)
			target := averageChroma(readPixel, bounds, mbx*16+bx*8, mby*16+by*8, cb)
			coeff := quantizeDC(int(target)-int(pred), vp8BaseQuantizerDC)
			blockNZ := encodeVP8Coeff(enc, vp8PlaneUV, nz+up[base+bx], coeff)
			recon := uint8(clipInt(int(pred)+dcDelta(coeff, vp8BaseQuantizerDC), 0, 255))
			fill4(rec, stride, x, y, recon)
			nz = blockNZ
			up[base+bx] = blockNZ
		}
		left[base+by] = nz
	}
}

func averageY(readPixel pixelReader, bounds image.Rectangle, x int, y int) uint8 {
	sum := 0
	for yy := 0; yy < 4; yy++ {
		for xx := 0; xx < 4; xx++ {
			c := samplePixel(readPixel, bounds, x+xx, y+yy)
			luma, _, _ := color.RGBToYCbCr(c.R, c.G, c.B)
			sum += int(luma)
		}
	}
	return uint8((sum + 8) / 16)
}

func averageChroma(readPixel pixelReader, bounds image.Rectangle, x int, y int, cb bool) uint8 {
	sum := 0
	for yy := 0; yy < 8; yy++ {
		for xx := 0; xx < 8; xx++ {
			c := samplePixel(readPixel, bounds, x+xx, y+yy)
			_, u, v := color.RGBToYCbCr(c.R, c.G, c.B)
			if cb {
				sum += int(u)
			} else {
				sum += int(v)
			}
		}
	}
	return uint8((sum + 32) / 64)
}

func samplePixel(readPixel pixelReader, bounds image.Rectangle, x int, y int) color.NRGBA {
	if x >= bounds.Dx() {
		x = bounds.Dx() - 1
	}
	if y >= bounds.Dy() {
		y = bounds.Dy() - 1
	}
	return readPixel(bounds.Min.X+x, bounds.Min.Y+y)
}

func pred4DC(rec []uint8, stride int, x int, y int) uint8 {
	sum := 4
	for i := 0; i < 4; i++ {
		if y == 0 {
			sum += 0x7f
		} else {
			sum += int(rec[(y-1)*stride+x+i])
		}
	}
	for j := 0; j < 4; j++ {
		if x == 0 {
			sum += 0x81
		} else {
			sum += int(rec[(y+j)*stride+x-1])
		}
	}
	return uint8(sum / 8)
}

func pred8DC(rec []uint8, stride int, mbx int, mby int, x int, y int) uint8 {
	leftX := mbx * 8
	topY := mby * 8
	switch {
	case mbx == 0 && mby == 0:
		return 0x80
	case mbx == 0:
		sum := 4
		for i := 0; i < 8; i++ {
			sum += int(rec[(topY-1)*stride+leftX+i])
		}
		return uint8(sum / 8)
	case mby == 0:
		sum := 4
		for j := 0; j < 8; j++ {
			sum += int(rec[(topY+j)*stride+leftX-1])
		}
		return uint8(sum / 8)
	default:
		sum := 8
		for i := 0; i < 8; i++ {
			sum += int(rec[(topY-1)*stride+leftX+i])
		}
		for j := 0; j < 8; j++ {
			sum += int(rec[(topY+j)*stride+leftX-1])
		}
		return uint8(sum / 16)
	}
}

func fill4(dst []uint8, stride int, x int, y int, v uint8) {
	for yy := 0; yy < 4; yy++ {
		row := dst[(y+yy)*stride+x:]
		row[0] = v
		row[1] = v
		row[2] = v
		row[3] = v
	}
}

func quantizeDC(delta int, q int) int {
	estimate := 0
	if q != 0 {
		estimate = delta * 8 / q
	}
	best := estimate
	bestErr := absInt(delta - dcDelta(best, q))
	for coeff := estimate - 4; coeff <= estimate+4; coeff++ {
		if coeff < -2047 || coeff > 2047 {
			continue
		}
		err := absInt(delta - dcDelta(coeff, q))
		if err < bestErr {
			best = coeff
			bestErr = err
		}
	}
	return clipInt(best, -2047, 2047)
}

func dcDelta(coeff int, q int) int {
	return (coeff*q + 4) >> 3
}

func clipInt(v int, min int, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
