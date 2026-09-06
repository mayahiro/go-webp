// Package webp encodes images in WebP format.
package webp

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"io"
)

const (
	maxVP8LDimension = 16384
	maxVP8Dimension  = 16383

	nLiteralCodes  = 256
	nLengthCodes   = 24
	nDistanceCodes = 40

	vp8lMinBackwardRefLength = 4
	vp8lMaxBackwardRefLength = 4096
	vp8lMaxDistanceCode      = 1048576
	vp8lMaxColorCacheBits    = 11
)

// Compression selects the WebP bitstream written by Encode.
type Compression int

const (
	// CompressionLossless writes a VP8L lossless WebP image.
	CompressionLossless Compression = iota
	// CompressionLossy writes a VP8-based lossy WebP image.
	CompressionLossy
)

// Mode selects an encoder search profile or an explicit encoding family.
//
// ModeDefault preserves the historical Options behavior. The other modes tune
// the selected Compression, except ModeNearLossless and ModeLossyQuality, which
// select VP8L near-lossless and VP8 lossy output respectively.
type Mode int

const (
	// ModeDefault uses the default balanced encoder behavior.
	ModeDefault Mode = iota
	// ModeFast uses a streaming lossless search with inexpensive transforms and matches.
	ModeFast
	// ModeBalanced uses the default balance of size, speed, and memory.
	ModeBalanced
	// ModeBestCompression broadens bounded transform, match, and entropy search.
	ModeBestCompression
	// ModeLowMemory avoids buffered lossless candidates and lossy residual buffering.
	ModeLowMemory
	// ModeNearLossless writes VP8L with alpha preserved and edge-aware RGB
	// quantization controlled by Quality. Quality 100, or an omitted Quality,
	// is equivalent to lossless.
	ModeNearLossless
	// ModeLossyQuality writes VP8 lossy output and uses Quality for quality control.
	ModeLossyQuality
	// ModeAuto lets the encoder select an internal profile. It currently uses
	// image features for lossless encoding and the default profile for lossy
	// encoding. The selected profile and encoded bytes may change between releases.
	ModeAuto
)

// Options are the encoding parameters for Encode.
//
// A nil Options value and the zero value both write VP8L lossless WebP images.
type Options struct {
	// Compression selects lossless or lossy WebP encoding.
	Compression Compression
	// Quality controls lossy WebP quality from 1 to 100. Values less than or
	// equal to zero use the default, and values greater than 100 are clamped to
	// 100. Quality is ignored for ordinary lossless encoding. In ModeNearLossless,
	// Quality controls edge-aware RGB quantization and the default is 100.
	Quality int
	// Mode selects an encoder search profile. ModeDefault preserves the behavior
	// selected by Compression and Quality.
	Mode Mode
}

// Encoder writes WebP images.
type Encoder struct {
	// Options configures the encoder. A nil Options value uses the default
	// lossless settings.
	Options *Options
}

// Encode writes the image m to w in WebP format.
// Write errors from w are returned unchanged. If writing fails, w may contain
// a partial image.
func Encode(w io.Writer, m image.Image, o *Options) error {
	return encodeWithMetadata(w, m, o, nil, nil)
}

func encodeWithMetadata(w io.Writer, m image.Image, o *Options, cancel *encodeCancellation, metadata *Metadata) error {
	if w == nil {
		return errors.New("webp: nil writer")
	}
	if m == nil {
		return errors.New("webp: nil image")
	}
	preparedMetadata, err := prepareMetadata(metadata)
	if err != nil {
		return err
	}

	source := newEncoderSource(m)
	source.cancel = cancel
	cancel.check()
	if cancel != nil {
		w = cancellingWriter{writer: w, cancel: cancel}
	}
	if source.width <= 0 || source.height <= 0 {
		return fmt.Errorf("webp: invalid image dimensions %dx%d", source.width, source.height)
	}
	mode := encodingMode(o)
	if !validMode(mode) {
		return fmt.Errorf("webp: unsupported encoding mode %d", mode)
	}
	switch mode {
	case ModeNearLossless:
		return encodeNearLosslessMetadata(w, source, nearLosslessQuality(o), preparedMetadata)
	case ModeLossyQuality:
		return encodeLossyMetadata(w, source, lossyQuality(o), mode, preparedMetadata)
	}
	switch compression(o) {
	case CompressionLossless:
		return encodeLosslessMetadata(w, source, mode, preparedMetadata)
	case CompressionLossy:
		return encodeLossyMetadata(w, source, lossyQuality(o), mode, preparedMetadata)
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

func encodingMode(o *Options) Mode {
	if o == nil {
		return ModeDefault
	}
	return o.Mode
}

func validMode(mode Mode) bool {
	return mode >= ModeDefault && mode <= ModeAuto
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

func nearLosslessQuality(o *Options) int {
	if o == nil || o.Quality <= 0 {
		return 100
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
