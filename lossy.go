package webp

import (
	"bufio"
	"fmt"
	"image"
	"io"
	"math"
	"runtime"
)

const (
	defaultLossyQuality  = 100
	vp8FirstPartitionMax = 1<<19 - 1
	vp8xAlphaFlag        = 0x10
	vp8xPayloadSize      = 10
	vp8ChromaCacheMinDim = 256

	alphCompressionNone  = 0
	alphCompressionVP8L  = 1
	alphFilterNone       = 0
	alphFilterHorizontal = 1
	alphFilterVertical   = 2
	alphFilterGradient   = 3

	alphaMinBackwardRefLength = 4
	alphaMaxBackwardRefLength = 4096
	alphaDistanceAbove        = 1
	alphaDistancePrevious     = 2
	alphaDistanceTopLeft      = 3
	alphaDistanceTopRight     = 4

	alphaCodeLengthCodeCount      = 19
	alphaCodeLengthCodeMaxLength  = 7
	alphaCodeLengthCodeKraft      = 1 << alphaCodeLengthCodeMaxLength
	alphaCodeLengthRepeatPrevious = 16
	alphaCodeLengthRepeatZero     = 17
	alphaCodeLengthRepeatZeroBig  = 18
)

func encodeLossy(w io.Writer, source encoderSource, quality int, mode Mode) error {
	return encodeLossyConfig(w, source, vp8LossyConfigForModeQuality(mode, quality), lossyAlphaConfigForMode(mode))
}

func encodeLossyConfig(w io.Writer, source encoderSource, lossyConfig vp8LossyConfig, alphaConfig lossyAlphaConfig) error {
	if source.width > maxVP8Dimension || source.height > maxVP8Dimension {
		return fmt.Errorf("webp: image dimensions %dx%d exceed VP8 limit %dx%d", source.width, source.height, maxVP8Dimension, maxVP8Dimension)
	}

	var defaultFrame []byte
	var frameSource vp8Source
	if lossyConfig.defaultFrameIncumbent {
		defaultConfig := makeVP8LossyConfig(lossyConfig.qualityProfile(), vp8EffortProfileForModeQIndex(ModeDefault, lossyConfig.qIndex))
		defaultSource := newVP8Source(source, defaultConfig.materializeSource)
		if defaultConfig.sharpYUV && defaultSource.materialized() {
			defaultSource.applySharpChroma(instrumentLossyPixelReader(source.pixels()))
		}
		var err error
		defaultFrame, err = encodeVP8KeyFrameSource(defaultSource, defaultConfig)
		if err != nil {
			return err
		}
		if defaultSource.materialized() && lossyConfig.materializeSource && defaultConfig.sharpYUV == lossyConfig.sharpYUV {
			// Frame searches only read the prepared plane; matching conversion settings can share it.
			frameSource = defaultSource
		}
	}

	if !frameSource.materialized() {
		frameSource = newVP8Source(source, lossyConfig.materializeSource)
		if lossyConfig.sharpYUV && frameSource.materialized() {
			frameSource.applySharpChroma(instrumentLossyPixelReader(source.pixels()))
		}
	}
	var alphaAnalysis lossyAlphaAnalysis
	var readPixel pixelReader
	var alphaDone chan lossyAlphaAnalysis
	if !lossyStandardImageOpaque(source.image) {
		readPixel = instrumentLossyPixelReader(source.pixels())
		if lossyCanParallelizeAlpha(source, lossyConfig) {
			alphaDone = make(chan lossyAlphaAnalysis, 1)
			go func(done chan<- lossyAlphaAnalysis) {
				var analysis lossyAlphaAnalysis
				source.cancel.run(func() {
					analysis = analyzeLossyAlphaConfig(readPixel, source.bounds, source.width, source.height, alphaConfig)
				})
				done <- analysis
			}(alphaDone)
			defer func() {
				if alphaDone != nil {
					<-alphaDone
				}
			}()
		} else {
			alphaAnalysis = analyzeLossyAlphaConfig(readPixel, source.bounds, source.width, source.height, alphaConfig)
		}
	}
	frame, err := encodeVP8KeyFrameSource(frameSource, lossyConfig)
	if alphaDone != nil {
		alphaAnalysis = <-alphaDone
		alphaDone = nil
	}
	source.cancel.check()
	if err != nil {
		return err
	}
	if defaultFrame != nil && len(defaultFrame) <= len(frame) {
		frame = defaultFrame
	}
	setLossyCounter(lossyCounterFirstPartitionBits, uint64(vp8FrameFirstPartitionBytes(frame))*8)
	if alphaAnalysis.hasAlpha {
		return writeLossyExtended(w, readPixel, source.bounds, source.width, source.height, frame, alphaAnalysis, alphaConfig)
	}
	return writeLossySimple(w, frame)
}

func lossyCanParallelizeAlpha(source encoderSource, cfg vp8LossyConfig) bool {
	if !cfg.parallelAlpha || source.width*source.height < 64*64 || runtime.GOMAXPROCS(0) < 2 {
		return false
	}
	return lossySourceSupportsParallelRead(source.image)
}

func lossySourceSupportsParallelRead(m image.Image) bool {
	return standardImageSupportsConcurrentRead(m)
}

func lossyStandardImageOpaque(m image.Image) bool {
	switch img := m.(type) {
	case *image.NRGBA:
		return img.Opaque()
	case *image.RGBA:
		return img.Opaque()
	case *image.NRGBA64:
		return img.Opaque()
	case *image.RGBA64:
		return img.Opaque()
	case *image.Gray:
		return img.Opaque()
	case *image.Gray16:
		return img.Opaque()
	case *image.YCbCr:
		return img.Opaque()
	case *image.Paletted:
		return img.Opaque()
	case *image.Uniform:
		return img.Opaque()
	default:
		return false
	}
}

func writeLossySimple(w io.Writer, frame []byte) error {
	payloadSize := uint64(len(frame))
	riffSize := uint64(4) + riffChunkSize(payloadSize)
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
	if err := writeChunkPadding(bw, payloadSize); err != nil {
		return err
	}
	return bw.Flush()
}

func writeLossyExtended(w io.Writer, readPixel pixelReader, bounds image.Rectangle, width int, height int, frame []byte, alphaAnalysis lossyAlphaAnalysis, alphaConfig lossyAlphaConfig) error {
	framePayloadSize := uint64(len(frame))
	alphaPayload, err := makeAlphaPayload(readPixel, bounds, width, height, alphaAnalysis, alphaConfig)
	if err != nil {
		return err
	}
	if framePayloadSize > math.MaxUint32 || alphaPayload.size > math.MaxUint32 {
		return fmt.Errorf("webp: encoded image is too large")
	}

	riffSize := uint64(4) + riffChunkSize(vp8xPayloadSize) + riffChunkSize(alphaPayload.size) + riffChunkSize(framePayloadSize)
	if riffSize > math.MaxUint32 {
		return fmt.Errorf("webp: encoded image is too large")
	}

	bw := bufio.NewWriter(w)
	if err := writeRIFFHeader(bw, uint32(riffSize)); err != nil {
		return err
	}
	if err := writeVP8XChunk(bw, width, height); err != nil {
		return err
	}
	if err := writeAlphaChunk(bw, readPixel, bounds, alphaPayload); err != nil {
		return err
	}
	if err := writeChunkHeader(bw, "VP8 ", uint32(framePayloadSize)); err != nil {
		return err
	}
	if _, err := bw.Write(frame); err != nil {
		return err
	}
	if err := writeChunkPadding(bw, framePayloadSize); err != nil {
		return err
	}
	return bw.Flush()
}

func writeVP8XChunk(w *bufio.Writer, width int, height int) error {
	if err := writeChunkHeader(w, "VP8X", vp8xPayloadSize); err != nil {
		return err
	}
	if err := w.WriteByte(vp8xAlphaFlag); err != nil {
		return err
	}
	if err := w.WriteByte(0); err != nil {
		return err
	}
	if err := w.WriteByte(0); err != nil {
		return err
	}
	if err := w.WriteByte(0); err != nil {
		return err
	}
	if err := writeUint24LE(w, uint32(width-1)); err != nil {
		return err
	}
	return writeUint24LE(w, uint32(height-1))
}
