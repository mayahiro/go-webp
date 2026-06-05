// Package webp encodes images in WebP format.
package webp

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
)

const (
	maxVP8LDimension = 16384
	maxVP8Dimension  = 16383

	nLiteralCodes  = 256
	nLengthCodes   = 24
	nDistanceCodes = 40
)

// Compression selects the WebP bitstream written by Encode.
type Compression int

const (
	// CompressionLossless writes a VP8L lossless WebP image.
	CompressionLossless Compression = iota
	// CompressionLossy writes a VP8-based lossy WebP image.
	CompressionLossy
)

// Options are the encoding parameters for Encode.
//
// A nil Options value and the zero value both write VP8L lossless WebP images.
type Options struct {
	// Compression selects lossless or lossy WebP encoding.
	Compression Compression
	// Quality controls lossy WebP quality from 1 to 100. Values less than or
	// equal to zero use the default, and values greater than 100 are clamped to
	// 100. Quality is ignored for lossless encoding.
	Quality int
}

// Encoder writes WebP images.
type Encoder struct {
	// Options configures the encoder. A nil Options value uses the default
	// lossless settings.
	Options *Options
}

// Encode writes the image m to w in WebP format.
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
	switch compression(o) {
	case CompressionLossless:
		return encodeLossless(w, m, bounds, width, height)
	case CompressionLossy:
		return encodeLossy(w, m, bounds, width, height, lossyQuality(o))
	default:
		return fmt.Errorf("webp: unsupported compression mode %d", compression(o))
	}
}

// Encode writes the image m to w in WebP format.
func (enc *Encoder) Encode(w io.Writer, m image.Image) error {
	if enc == nil {
		return Encode(w, m, nil)
	}
	return Encode(w, m, enc.Options)
}

func compression(o *Options) Compression {
	if o == nil {
		return CompressionLossless
	}
	return o.Compression
}

func lossyQuality(o *Options) int {
	if o == nil || o.Quality <= 0 {
		return defaultLossyQuality
	}
	if o.Quality > 100 {
		return 100
	}
	return o.Quality
}

func writeWebPHeader(w *bufio.Writer, chunk string, riffSize uint32, payloadSize uint32) error {
	if err := writeRIFFHeader(w, riffSize); err != nil {
		return err
	}
	return writeChunkHeader(w, chunk, payloadSize)
}

func writeRIFFHeader(w *bufio.Writer, riffSize uint32) error {
	if _, err := w.WriteString("RIFF"); err != nil {
		return err
	}
	if err := writeUint32LE(w, riffSize); err != nil {
		return err
	}
	_, err := w.WriteString("WEBP")
	return err
}

func writeChunkHeader(w *bufio.Writer, chunk string, payloadSize uint32) error {
	if _, err := w.WriteString(chunk); err != nil {
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

func writeUint24LE(w *bufio.Writer, v uint32) error {
	if err := w.WriteByte(byte(v)); err != nil {
		return err
	}
	if err := w.WriteByte(byte(v >> 8)); err != nil {
		return err
	}
	return w.WriteByte(byte(v >> 16))
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
type lumaReader func(x int, y int) uint8
type chromaReader func(x int, y int) (uint8, uint8)

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
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) color.NRGBA {
			i := (y-minY)*stride + (x-minX)*4
			return color.NRGBA{
				R: pix[i+0],
				G: pix[i+1],
				B: pix[i+2],
				A: pix[i+3],
			}
		}
	case *image.RGBA:
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) color.NRGBA {
			i := (y-minY)*stride + (x-minX)*4
			return nrgbaFromRGBA(pix[i+0], pix[i+1], pix[i+2], pix[i+3])
		}
	case *image.Gray:
		return func(x int, y int) color.NRGBA {
			gray := img.GrayAt(x, y).Y
			return color.NRGBA{R: gray, G: gray, B: gray, A: 255}
		}
	case *image.YCbCr:
		yPix := img.Y
		cbPix := img.Cb
		crPix := img.Cr
		return func(x int, y int) color.NRGBA {
			yy := yPix[img.YOffset(x, y)]
			ci := img.COffset(x, y)
			cb := cbPix[ci]
			cr := crPix[ci]
			r, g, b := color.YCbCrToRGB(yy, cb, cr)
			return color.NRGBA{R: r, G: g, B: b, A: 255}
		}
	case *image.Paletted:
		if len(img.Palette) == 0 {
			return func(x int, y int) color.NRGBA {
				return color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			}
		}
		palette := make([]color.NRGBA, len(img.Palette))
		for i, c := range img.Palette {
			palette[i] = color.NRGBAModel.Convert(c).(color.NRGBA)
		}
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) color.NRGBA {
			return palette[pix[(y-minY)*stride+x-minX]]
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

func lumaReaderFor(m image.Image) lumaReader {
	switch img := m.(type) {
	case *image.NRGBA:
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) uint8 {
			i := (y-minY)*stride + (x-minX)*4
			return rgbToLuma(pix[i+0], pix[i+1], pix[i+2])
		}
	case *image.RGBA:
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) uint8 {
			i := (y-minY)*stride + (x-minX)*4
			c := nrgbaFromRGBA(pix[i+0], pix[i+1], pix[i+2], pix[i+3])
			return rgbToLuma(c.R, c.G, c.B)
		}
	case *image.Gray:
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) uint8 {
			return pix[(y-minY)*stride+x-minX]
		}
	case *image.YCbCr:
		yPix := img.Y
		cbPix := img.Cb
		crPix := img.Cr
		return func(x int, y int) uint8 {
			yy := yPix[img.YOffset(x, y)]
			ci := img.COffset(x, y)
			cb := cbPix[ci]
			cr := crPix[ci]
			r, g, b := color.YCbCrToRGB(yy, cb, cr)
			return rgbToLuma(r, g, b)
		}
	case *image.Paletted:
		if len(img.Palette) == 0 {
			readPixel := pixelReaderFor(m)
			return func(x int, y int) uint8 {
				c := readPixel(x, y)
				return rgbToLuma(c.R, c.G, c.B)
			}
		}
		palette := make([]uint8, len(img.Palette))
		for i, c := range img.Palette {
			nrgba := color.NRGBAModel.Convert(c).(color.NRGBA)
			palette[i] = rgbToLuma(nrgba.R, nrgba.G, nrgba.B)
		}
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) uint8 {
			return palette[pix[(y-minY)*stride+x-minX]]
		}
	case *image.Uniform:
		c := color.NRGBAModel.Convert(img.C).(color.NRGBA)
		y := rgbToLuma(c.R, c.G, c.B)
		return func(int, int) uint8 {
			return y
		}
	default:
		readPixel := pixelReaderFor(m)
		return func(x int, y int) uint8 {
			c := readPixel(x, y)
			return rgbToLuma(c.R, c.G, c.B)
		}
	}
}

func chromaReaderFor(m image.Image) chromaReader {
	switch img := m.(type) {
	case *image.NRGBA:
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) (uint8, uint8) {
			i := (y-minY)*stride + (x-minX)*4
			return rgbToChroma(pix[i+0], pix[i+1], pix[i+2])
		}
	case *image.RGBA:
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) (uint8, uint8) {
			i := (y-minY)*stride + (x-minX)*4
			c := nrgbaFromRGBA(pix[i+0], pix[i+1], pix[i+2], pix[i+3])
			return rgbToChroma(c.R, c.G, c.B)
		}
	case *image.Gray:
		return func(int, int) (uint8, uint8) {
			return 128, 128
		}
	case *image.YCbCr:
		yPix := img.Y
		cbPix := img.Cb
		crPix := img.Cr
		return func(x int, y int) (uint8, uint8) {
			yy := yPix[img.YOffset(x, y)]
			ci := img.COffset(x, y)
			cb := cbPix[ci]
			cr := crPix[ci]
			r, g, b := color.YCbCrToRGB(yy, cb, cr)
			return rgbToChroma(r, g, b)
		}
	case *image.Paletted:
		if len(img.Palette) == 0 {
			readPixel := pixelReaderFor(m)
			return func(x int, y int) (uint8, uint8) {
				c := readPixel(x, y)
				return rgbToChroma(c.R, c.G, c.B)
			}
		}
		palette := make([][2]uint8, len(img.Palette))
		for i, c := range img.Palette {
			nrgba := color.NRGBAModel.Convert(c).(color.NRGBA)
			palette[i][0], palette[i][1] = rgbToChroma(nrgba.R, nrgba.G, nrgba.B)
		}
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) (uint8, uint8) {
			index := pix[(y-minY)*stride+x-minX]
			pair := palette[index]
			return pair[0], pair[1]
		}
	case *image.Uniform:
		c := color.NRGBAModel.Convert(img.C).(color.NRGBA)
		cb, cr := rgbToChroma(c.R, c.G, c.B)
		return func(int, int) (uint8, uint8) {
			return cb, cr
		}
	default:
		readPixel := pixelReaderFor(m)
		return func(x int, y int) (uint8, uint8) {
			c := readPixel(x, y)
			return rgbToChroma(c.R, c.G, c.B)
		}
	}
}

func nrgbaFromRGBA(r uint8, g uint8, b uint8, a uint8) color.NRGBA {
	if a == 0xff {
		return color.NRGBA{R: r, G: g, B: b, A: 0xff}
	}
	if a == 0 {
		return color.NRGBA{}
	}
	r16 := uint32(r)
	r16 |= r16 << 8
	g16 := uint32(g)
	g16 |= g16 << 8
	b16 := uint32(b)
	b16 |= b16 << 8
	a16 := uint32(a)
	a16 |= a16 << 8
	r16 = (r16 * 0xffff) / a16
	g16 = (g16 * 0xffff) / a16
	b16 = (b16 * 0xffff) / a16
	return color.NRGBA{R: uint8(r16 >> 8), G: uint8(g16 >> 8), B: uint8(b16 >> 8), A: a}
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
	writeVP8LHeader(bits, width, height, analysis.alpha)
	writeVP8LImageStream(bits, readPixel, bounds, analysis)
}

func writeVP8LHeader(bits *bitWriter, width int, height int, alpha bool) {
	bits.writeBits(0x2f, 8)
	bits.writeBits(uint32(width-1), 14)
	bits.writeBits(uint32(height-1), 14)
	if alpha {
		bits.writeBits(1, 1)
	} else {
		bits.writeBits(0, 1)
	}
	bits.writeBits(0, 3)
}

func writeVP8LImageStream(bits *bitWriter, readPixel pixelReader, bounds image.Rectangle, analysis imageAnalysis) {
	bits.writeBits(0, 1) // no transforms
	bits.writeBits(0, 1) // no color cache
	bits.writeBits(0, 1) // no meta prefix image

	writeChannelTree(bits, analysis.channels[0], nLiteralCodes+nLengthCodes)
	writeChannelTree(bits, analysis.channels[1], nLiteralCodes)
	writeChannelTree(bits, analysis.channels[2], nLiteralCodes)
	writeChannelTree(bits, analysis.channels[3], nLiteralCodes)
	writeSimpleTree(bits, 0)

	if analysis.allChannelsConstant() {
		return
	}
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

func (a imageAnalysis) allChannelsConstant() bool {
	for _, ch := range a.channels {
		if !ch.constant {
			return false
		}
	}
	return true
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

func writeTwoSymbolTree(bits *bitWriter, symbol0 uint8, symbol1 uint8) {
	bits.writeBits(1, 1)
	bits.writeBits(1, 1)
	if symbol0 < 2 {
		bits.writeBits(0, 1)
		bits.writeBits(uint32(symbol0), 1)
	} else {
		bits.writeBits(1, 1)
		bits.writeBits(uint32(symbol0), 8)
	}
	bits.writeBits(uint32(symbol1), 8)
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
