// Package webp encodes images in WebP format.
package webp

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
)

const (
	maxVP8LDimension = 16384

	nLiteralCodes  = 256
	nLengthCodes   = 24
	nDistanceCodes = 40
)

// Options are the encoding parameters for Encode.
//
// The current encoder writes VP8L lossless WebP images. The struct is reserved
// for future encoder options and may be nil.
type Options struct{}

// Encoder writes WebP images.
type Encoder struct {
	// Options configures the encoder. A nil Options value uses the default
	// lossless settings.
	Options *Options
}

// Encode writes the image m to w in WebP lossless format.
func Encode(w io.Writer, m image.Image, o *Options) error {
	if w == nil {
		return errors.New("webp: nil writer")
	}
	if m == nil {
		return errors.New("webp: nil image")
	}

	bounds := m.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 {
		return fmt.Errorf("webp: invalid image dimensions %dx%d", width, height)
	}
	if width > maxVP8LDimension || height > maxVP8LDimension {
		return fmt.Errorf("webp: image dimensions %dx%d exceed VP8L limit %dx%d", width, height, maxVP8LDimension, maxVP8LDimension)
	}

	readPixel := pixelReaderFor(m)
	analysis := analyzeImage(readPixel, bounds)
	payloadBits := vp8lPayloadBits(width, height, analysis)
	payloadSize := (payloadBits + 7) / 8
	padding := payloadSize & 1
	riffSize := uint64(4) + 8 + payloadSize + padding
	if riffSize > math.MaxUint32 {
		return fmt.Errorf("webp: encoded image is too large")
	}

	bw := bufio.NewWriter(w)
	if err := writeWebPHeader(bw, uint32(riffSize), uint32(payloadSize)); err != nil {
		return err
	}

	bits := newBitWriter(bw)
	writeVP8L(bits, readPixel, bounds, width, height, analysis)
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

// Encode writes the image m to w in WebP lossless format.
func (enc *Encoder) Encode(w io.Writer, m image.Image) error {
	if enc == nil {
		return Encode(w, m, nil)
	}
	return Encode(w, m, enc.Options)
}

func writeWebPHeader(w *bufio.Writer, riffSize uint32, payloadSize uint32) error {
	if _, err := w.WriteString("RIFF"); err != nil {
		return err
	}
	if err := writeUint32LE(w, riffSize); err != nil {
		return err
	}
	if _, err := w.WriteString("WEBP"); err != nil {
		return err
	}
	if _, err := w.WriteString("VP8L"); err != nil {
		return err
	}
	return writeUint32LE(w, payloadSize)
}

func writeUint32LE(w *bufio.Writer, v uint32) error {
	if err := w.WriteByte(byte(v)); err != nil {
		return err
	}
	if err := w.WriteByte(byte(v >> 8)); err != nil {
		return err
	}
	if err := w.WriteByte(byte(v >> 16)); err != nil {
		return err
	}
	return w.WriteByte(byte(v >> 24))
}

type imageAnalysis struct {
	channels [4]channelPlan
	alpha    bool
}

type channelPlan struct {
	constant bool
	value    uint8
}

type pixelReader func(x int, y int) color.NRGBA

func analyzeImage(readPixel pixelReader, bounds image.Rectangle) imageAnalysis {
	var a imageAnalysis
	first := true
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := readPixel(x, y)
			if first {
				a.channels[0] = channelPlan{constant: true, value: c.G}
				a.channels[1] = channelPlan{constant: true, value: c.R}
				a.channels[2] = channelPlan{constant: true, value: c.B}
				a.channels[3] = channelPlan{constant: true, value: c.A}
				first = false
			} else {
				a.channels[0].observe(c.G)
				a.channels[1].observe(c.R)
				a.channels[2].observe(c.B)
				a.channels[3].observe(c.A)
			}
			a.alpha = a.alpha || c.A != 255
		}
	}
	return a
}

func (p *channelPlan) observe(v uint8) {
	if p.constant && p.value != v {
		p.constant = false
	}
}

func pixelReaderFor(m image.Image) pixelReader {
	switch img := m.(type) {
	case *image.NRGBA:
		return func(x int, y int) color.NRGBA {
			i := img.PixOffset(x, y)
			return color.NRGBA{
				R: img.Pix[i+0],
				G: img.Pix[i+1],
				B: img.Pix[i+2],
				A: img.Pix[i+3],
			}
		}
	case *image.RGBA:
		return func(x int, y int) color.NRGBA {
			return color.NRGBAModel.Convert(img.RGBAAt(x, y)).(color.NRGBA)
		}
	case *image.Gray:
		return func(x int, y int) color.NRGBA {
			gray := img.GrayAt(x, y).Y
			return color.NRGBA{R: gray, G: gray, B: gray, A: 255}
		}
	case *image.YCbCr:
		return func(x int, y int) color.NRGBA {
			return color.NRGBAModel.Convert(img.YCbCrAt(x, y)).(color.NRGBA)
		}
	case *image.Paletted:
		return func(x int, y int) color.NRGBA {
			return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
		}
	case *image.Uniform:
		c := color.NRGBAModel.Convert(img.C).(color.NRGBA)
		return func(int, int) color.NRGBA {
			return c
		}
	default:
		return func(x int, y int) color.NRGBA {
			return color.NRGBAModel.Convert(m.At(x, y)).(color.NRGBA)
		}
	}
}

func vp8lPayloadBits(width int, height int, analysis imageAnalysis) uint64 {
	bits := uint64(0)
	bits += 8           // signature
	bits += 14 + 14 + 4 // dimensions, alpha hint, version
	bits += 1           // transform list terminator
	bits += 1           // no color cache
	bits += 1           // no meta prefix image

	bits += huffmanTreeBits(analysis.channels[0], nLiteralCodes+nLengthCodes)
	bits += huffmanTreeBits(analysis.channels[1], nLiteralCodes)
	bits += huffmanTreeBits(analysis.channels[2], nLiteralCodes)
	bits += huffmanTreeBits(analysis.channels[3], nLiteralCodes)
	bits += simpleTreeBits(0)

	pixelBits := uint64(0)
	for _, ch := range analysis.channels {
		if !ch.constant {
			pixelBits += 8
		}
	}
	return bits + uint64(width)*uint64(height)*pixelBits
}

func huffmanTreeBits(ch channelPlan, alphabetSize int) uint64 {
	if ch.constant {
		return simpleTreeBits(ch.value)
	}
	return full8TreeBits(alphabetSize)
}

func simpleTreeBits(symbol uint8) uint64 {
	if symbol < 2 {
		return 4
	}
	return 11
}

func full8TreeBits(alphabetSize int) uint64 {
	return 1 + 4 + 12*3 + 1 + uint64(alphabetSize)
}

func writeVP8L(bits *bitWriter, readPixel pixelReader, bounds image.Rectangle, width int, height int, analysis imageAnalysis) {
	bits.writeBits(0x2f, 8)
	bits.writeBits(uint32(width-1), 14)
	bits.writeBits(uint32(height-1), 14)
	if analysis.alpha {
		bits.writeBits(1, 1)
	} else {
		bits.writeBits(0, 1)
	}
	bits.writeBits(0, 3)

	bits.writeBits(0, 1) // no transforms
	bits.writeBits(0, 1) // no color cache
	bits.writeBits(0, 1) // no meta prefix image

	writeChannelTree(bits, analysis.channels[0], nLiteralCodes+nLengthCodes)
	writeChannelTree(bits, analysis.channels[1], nLiteralCodes)
	writeChannelTree(bits, analysis.channels[2], nLiteralCodes)
	writeChannelTree(bits, analysis.channels[3], nLiteralCodes)
	writeSimpleTree(bits, 0)

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := readPixel(x, y)
			writeChannelSymbol(bits, analysis.channels[0], c.G)
			writeChannelSymbol(bits, analysis.channels[1], c.R)
			writeChannelSymbol(bits, analysis.channels[2], c.B)
			writeChannelSymbol(bits, analysis.channels[3], c.A)
		}
	}
}

func writeChannelTree(bits *bitWriter, ch channelPlan, alphabetSize int) {
	if ch.constant {
		writeSimpleTree(bits, ch.value)
		return
	}
	writeFull8Tree(bits, alphabetSize)
}

func writeSimpleTree(bits *bitWriter, symbol uint8) {
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

func writeFull8Tree(bits *bitWriter, alphabetSize int) {
	bits.writeBits(0, 1)
	bits.writeBits(8, 4)
	for _, length := range full8CodeLengthCodeLengths {
		bits.writeBits(uint32(length), 3)
	}
	bits.writeBits(0, 1)
	for symbol := 0; symbol < alphabetSize; symbol++ {
		if symbol < nLiteralCodes {
			bits.writeBits(1, 1)
		} else {
			bits.writeBits(0, 1)
		}
	}
}

func writeChannelSymbol(bits *bitWriter, ch channelPlan, symbol uint8) {
	if ch.constant {
		return
	}
	bits.writeBits(uint32(reverse8(symbol)), 8)
}

var full8CodeLengthCodeLengths = [12]uint8{
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
