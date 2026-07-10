// Package webp encodes images in WebP format.
package webp

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"sort"
)

const (
	maxVP8LDimension = 16384
	maxVP8Dimension  = 16383

	nLiteralCodes  = 256
	nLengthCodes   = 24
	nDistanceCodes = 40

	vp8lMinBackwardRefLength          = 4
	vp8lMaxBackwardRefLength          = 4096
	vp8lHashBits                      = 12
	vp8lHashSize                      = 1 << vp8lHashBits
	vp8lMinHashCandidates             = 4
	vp8lMidHashCandidates             = 6
	vp8lMaxHashCandidates             = 8
	vp8lLazyMatchMinGain              = 2
	vp8lMaxDistanceCode               = 1048576
	vp8lMinColorCacheBits             = 1
	vp8lMaxColorCacheBits             = 6
	vp8lMaxColorCacheSize             = 1 << vp8lMaxColorCacheBits
	vp8lColorCacheSampleSize          = 2048
	vp8lMinMetaPrefixBits             = 2
	vp8lMaxMetaPrefixBits             = 9
	vp8lMinMetaPrefixCandidateBits    = 4
	vp8lMaxMetaPrefixGroups           = 16
	vp8lMaxMetaPrefixBlocks           = 1024
	vp8lMaxMetaPrefixColorCacheTokens = 1 << 18
	vp8lMaxChannelSmallSymbols        = 16
	vp8lMaxMaterializedIndexPixels    = 1 << 16
	nColorCacheGreenCodes             = nLiteralCodes + nLengthCodes + vp8lMaxColorCacheSize
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
	// ModeFast limits lossless search work to cheap literal and color-indexing paths.
	ModeFast
	// ModeBalanced uses the default balance of size, speed, and memory.
	ModeBalanced
	// ModeBestCompression keeps more compression candidates available, including
	// exhaustive color-indexing checks that default mode may skip with early exits.
	ModeBestCompression
	// ModeLowMemory avoids buffered lossless candidates and lossy residual buffering.
	ModeLowMemory
	// ModeNearLossless writes VP8L with alpha preserved and edge-aware RGB
	// quantization controlled by Quality. Quality 100, or an omitted Quality,
	// is equivalent to lossless.
	ModeNearLossless
	// ModeLossyQuality writes VP8 lossy output and uses Quality for quality control.
	ModeLossyQuality
	// ModeAuto chooses a conservative internal profile from simple image features.
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
func Encode(w io.Writer, m image.Image, o *Options) error {
	if w == nil {
		return errors.New("webp: nil writer")
	}
	if m == nil {
		return errors.New("webp: nil image")
	}

	source := newEncoderSource(m)
	if source.width <= 0 || source.height <= 0 {
		return fmt.Errorf("webp: invalid image dimensions %dx%d", source.width, source.height)
	}
	mode := encodingMode(o)
	if !validMode(mode) {
		return fmt.Errorf("webp: unsupported encoding mode %d", mode)
	}
	switch mode {
	case ModeNearLossless:
		return encodeNearLossless(w, source, nearLosslessQuality(o), mode)
	case ModeLossyQuality:
		return encodeLossy(w, source, lossyQuality(o), mode)
	}
	switch compression(o) {
	case CompressionLossless:
		return encodeLossless(w, source, mode)
	case CompressionLossy:
		return encodeLossy(w, source, lossyQuality(o), mode)
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

type imageAnalysis struct {
	channels [4]channelPlan
	alpha    bool
}

type vp8lEncodingPlan struct {
	analysis            imageAnalysis
	alpha               bool
	predictor           bool
	predictorMode       uint8
	predictorSizeBits   uint8
	predictorImage      []uint8
	predictorAnalysis   imageAnalysis
	colorTransform      bool
	colorSizeBits       uint8
	colorElement        vp8lColorTransformElement
	colorAnalysis       imageAnalysis
	colorIndexing       bool
	colorIndexWidthBits uint8
	colorTable          []color.NRGBA
	colorIndex          map[color.NRGBA]uint8
	colorIndexReader    pixelReader
	colorIndexAnalysis  imageAnalysis
	metaPrefix          *vp8lMetaPrefixPlan
	colorCache          *vp8lColorCachePlan
	lz77                bool
	lz77Tokens          []vp8lToken
	lz77LiteralAnalysis imageAnalysis
	lz77GreenCounts     [nLiteralCodes + nLengthCodes]uint32
	lz77GreenLengths    [nLiteralCodes + nLengthCodes]uint8
	lz77GreenCodes      [nLiteralCodes + nLengthCodes]uint16
	lz77DistanceCounts  [nDistanceCodes]uint32
	lz77DistanceN       int
	lz77DistanceSymbols [2]uint8
	lz77DistanceLengths [nDistanceCodes]uint8
	lz77DistanceCodes   [nDistanceCodes]uint16
	lz77DistanceNormal  bool
	subtractGreen       bool
}

type vp8lColorTransformElement struct {
	greenToRed  uint8
	greenToBlue uint8
	redToBlue   uint8
}

type vp8lColorCachePlan struct {
	bits     uint8
	tokens   []vp8lToken
	analysis imageAnalysis
	counts   [nColorCacheGreenCodes]uint32
	lengths  [nColorCacheGreenCodes]uint8
	codes    [nColorCacheGreenCodes]uint16
}

type vp8lMetaPrefixPlan struct {
	prefixBits       uint8
	width            int
	height           int
	image            []uint16
	imageAnalysis    imageAnalysis
	groups           []imageAnalysis
	groupPixels      []int
	colorCacheGroups []vp8lColorCacheGroupPlan
	lz77Groups       []vp8lLZ77GroupPlan
	groupTokens      []int
}

type vp8lColorCacheGroupPlan struct {
	literalAnalysis imageAnalysis
	counts          [nColorCacheGreenCodes]uint32
	lengths         [nColorCacheGreenCodes]uint8
	codes           [nColorCacheGreenCodes]uint16
}

type vp8lLZ77GroupPlan struct {
	literalAnalysis imageAnalysis
	greenCounts     [nLiteralCodes + nLengthCodes]uint32
	greenLengths    [nLiteralCodes + nLengthCodes]uint8
	greenCodes      [nLiteralCodes + nLengthCodes]uint16
	distanceCounts  [nDistanceCodes]uint32
	distanceN       int
	distanceSymbols [2]uint8
	distanceLengths [nDistanceCodes]uint8
	distanceCodes   [nDistanceCodes]uint16
	distanceNormal  bool
}

type vp8lToken struct {
	pixel        color.NRGBA
	copyLength   int
	distanceCode int
	cacheIndex   int
	colorCache   bool
}

type vp8lMatch struct {
	length       int
	distance     int
	distanceCode int
}

type vp8lEncodingConfig struct {
	allowColorIndexEarlyExit        bool
	tryTransforms                   bool
	tryCombinedTransforms           bool
	tryLZ77                         bool
	tryMetaPrefix                   bool
	tryColorCache                   bool
	tryLZ77ColorCache               bool
	tryLZ77MetaPrefix               bool
	tryLZ77TokenMetaPrefix          bool
	optimalLZ77Passes               int
	maxOptimalLZ77Pixels            int
	tryTransformedLZ77ColorCache    bool
	tryBlockPredictor               bool
	minColorIndexEarlyExitPixels    int
	palettedColorIndexEarlyExitRate uint64
	nrgbaColorIndexEarlyExitRate    uint64
	predictorModes                  []uint8
	predictorBlockSizeBits          []uint8
	colorTransformCandidates        []vp8lColorTransformElement
	prioritizeColorIndexCandidate   bool
	maxMetaPrefixLZ77Tokens         int
	maxTransformedLZ77CacheTokens   int
	parallelTransforms              bool
}

type vp8lAutoLosslessReason int

const (
	vp8lAutoLosslessReasonBalanced vp8lAutoLosslessReason = iota
	vp8lAutoLosslessReasonLargeLowColor
	vp8lAutoLosslessReasonHugeImage
	vp8lAutoLosslessReasonPaletteLike
	vp8lAutoLosslessReasonFlat
	vp8lAutoLosslessReasonUILike
	vp8lAutoLosslessReasonGradientLike
	vp8lAutoLosslessReasonPhotoLike
	vp8lAutoLosslessReasonAlphaHeavy
)

type channelPlan struct {
	constant             bool
	value                uint8
	n                    int
	symbols              [vp8lMaxChannelSmallSymbols]uint8
	counts               [vp8lMaxChannelSmallSymbols]uint32
	lengths              [vp8lMaxChannelSmallSymbols]uint8
	codes                [vp8lMaxChannelSmallSymbols]uint16
	normal               bool
	normalTreeBaseBits   uint32
	normalTreeTokenCount uint16
	normalCostCached     bool
	normalDataBits       uint64
	last                 uint8
	lastPos              uint8
	lastOK               bool
}

func analyzeImage(readPixel pixelReader, bounds image.Rectangle) imageAnalysis {
	var a imageAnalysis
	first := true
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := readPixel(x, y)
			if first {
				a.channels[0] = newConstantChannelPlan(c.G)
				a.channels[1] = newConstantChannelPlan(c.R)
				a.channels[2] = newConstantChannelPlan(c.B)
				a.channels[3] = newConstantChannelPlan(c.A)
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
	a.finalizeChannels()
	return a
}

func newConstantChannelPlan(v uint8) channelPlan {
	var counts [vp8lMaxChannelSmallSymbols]uint32
	counts[0] = 1
	return channelPlan{
		constant: true,
		value:    v,
		n:        1,
		symbols:  [vp8lMaxChannelSmallSymbols]uint8{v},
		counts:   counts,
		last:     v,
		lastOK:   true,
	}
}

func (p *channelPlan) observe(v uint8) {
	if p.constant && p.value != v {
		p.constant = false
	}
	p.observeSymbol(v)
}

func (p *channelPlan) observeSymbol(v uint8) {
	p.observeSymbolCount(v, 1)
}

func (p *channelPlan) observeSymbolCount(v uint8, count uint32) {
	if p.n < 0 {
		return
	}
	if p.lastOK && p.last == v {
		p.counts[p.lastPos] += count
		return
	}
	for i := 0; i < p.n; i++ {
		if p.symbols[i] == v {
			p.counts[i] += count
			p.last = v
			p.lastPos = uint8(i)
			p.lastOK = true
			return
		}
	}
	if p.n >= len(p.symbols) {
		p.n = -1
		p.normal = false
		p.lastOK = false
		return
	}
	pos := p.n
	p.symbols[pos] = v
	p.counts[pos] = count
	p.n++
	for pos > 0 && p.symbols[pos] < p.symbols[pos-1] {
		p.symbols[pos], p.symbols[pos-1] = p.symbols[pos-1], p.symbols[pos]
		p.counts[pos], p.counts[pos-1] = p.counts[pos-1], p.counts[pos]
		pos--
	}
	p.last = v
	p.lastPos = uint8(pos)
	p.lastOK = true
}

func (a *imageAnalysis) finalizeChannels() {
	for i := range a.channels {
		a.channels[i].finalize()
	}
}

func (p *channelPlan) finalize() {
	p.normal = false
	p.normalTreeBaseBits = 0
	p.normalTreeTokenCount = 0
	p.normalDataBits = 0
	p.normalCostCached = false
	clear(p.lengths[:])
	clear(p.codes[:])
	if p.constant || p.n < 3 {
		return
	}
	var counts [nLiteralCodes]uint32
	for i := 0; i < p.n; i++ {
		counts[p.symbols[i]] = p.counts[i]
	}
	var lengths [nLiteralCodes]uint8
	if !huffmanCodeLengthsInto(lengths[:], counts[:]) {
		return
	}
	codes := canonicalChannelCodes(lengths[:])
	for i := 0; i < p.n; i++ {
		symbol := p.symbols[i]
		p.lengths[i] = lengths[symbol]
		p.codes[i] = codes[symbol]
		p.normalDataBits += uint64(p.counts[i]) * uint64(p.lengths[i])
	}
	normalTreeBaseBits, normalTreeTokenCount := alphaNormalTreeBaseBits(lengths[:])
	p.normalTreeBaseBits = uint32(normalTreeBaseBits)
	p.normalTreeTokenCount = normalTreeTokenCount
	p.normalCostCached = true
	p.normal = true
}

func chooseVP8LEncodingPlan(readPixel pixelReader, bounds image.Rectangle, width int, height int) vp8lEncodingPlan {
	return chooseVP8LEncodingPlanForImage(nil, readPixel, bounds, width, height)
}

func chooseVP8LEncodingPlanForImage(m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, height int) vp8lEncodingPlan {
	return chooseVP8LEncodingPlanForImageWithConfig(m, readPixel, bounds, width, height, vp8lDefaultEncodingConfig())
}

func chooseVP8LEncodingPlanForImageExhaustive(m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, height int) vp8lEncodingPlan {
	return chooseVP8LEncodingPlanForImageConfig(m, readPixel, bounds, width, height, false)
}

func chooseVP8LEncodingPlanForImageConfig(m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, height int, allowPalettedColorIndexEarlyExit bool) vp8lEncodingPlan {
	cfg := vp8lDefaultEncodingConfig()
	cfg.allowColorIndexEarlyExit = allowPalettedColorIndexEarlyExit
	return chooseVP8LEncodingPlanForImageWithConfig(m, readPixel, bounds, width, height, cfg)
}

func chooseVP8LEncodingPlanForImageMode(m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, height int, mode Mode) vp8lEncodingPlan {
	return chooseVP8LEncodingPlanForImageWithConfig(m, readPixel, bounds, width, height, vp8lEncodingConfigForMode(mode, m, readPixel, bounds, width, height))
}

func chooseVP8LEncodingPlanForImageWithConfig(m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, height int, cfg vp8lEncodingConfig) vp8lEncodingPlan {
	readPixel, cfg.parallelTransforms = vp8lPrepareParallelTransformReader(m, readPixel, bounds, width, height, cfg.parallelTransforms)
	analysis := analyzeImage(readPixel, bounds)
	best := vp8lEncodingPlan{
		analysis: analysis,
		alpha:    analysis.alpha,
	}
	literalPlan := best
	bestBits := vp8lPayloadBits(width, height, best)
	var candidates [vp8lMaxEncodingPlanCandidates]vp8lEncodingPlan
	candidateCount := 0
	literalBestIndex := 0
	candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(&candidates, candidateCount, literalBestIndex, best, width, height, best, bestBits)

	colorIndexPlan, colorIndexOK := makeVP8LColorIndexingPlanForImage(m, readPixel, bounds, width, height, analysis.alpha)
	indexedPredictorPlan := vp8lEncodingPlan{}
	indexedPredictorOK := false
	if colorIndexOK && cfg.tryTransforms {
		indexedPredictorPlan, indexedPredictorOK = makeVP8LIndexedPredictorPlan(readPixel, bounds, width, height, colorIndexPlan, cfg)
	}
	if colorIndexOK && cfg.allowColorIndexEarlyExit {
		colorIndexBits := vp8lPayloadBits(width, height, colorIndexPlan)
		if vp8lShouldUseColorIndexEarlyExitConfig(m, colorIndexPlan, colorIndexBits, bestBits, width, height, cfg) {
			candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(&candidates, candidateCount, literalBestIndex, colorIndexPlan, width, height, best, bestBits)
			if indexedPredictorOK {
				candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(&candidates, candidateCount, literalBestIndex, indexedPredictorPlan, width, height, best, bestBits)
			}
			return vp8lFinalizeEncodingPlan(readPixel, bounds, width, height, literalPlan, &candidates, candidateCount, literalBestIndex, best, bestBits, cfg)
		}
	}
	if colorIndexOK && cfg.prioritizeColorIndexCandidate {
		candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(&candidates, candidateCount, literalBestIndex, colorIndexPlan, width, height, best, bestBits)
		if indexedPredictorOK {
			candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(&candidates, candidateCount, literalBestIndex, indexedPredictorPlan, width, height, best, bestBits)
		}
		colorIndexOK = false
		indexedPredictorOK = false
	}

	if cfg.tryTransforms && cfg.parallelTransforms {
		candidateCount, literalBestIndex, best, bestBits = vp8lAddParallelTransformCandidates(
			readPixel, bounds, width, height, analysis, cfg,
			&candidates, candidateCount, literalBestIndex, best, bestBits,
		)
	}

	if cfg.tryTransforms && !cfg.parallelTransforms {
		if cfg.tryBlockPredictor {
			if blockPredictorPlan, ok := makeVP8LBlockPredictorPlan(readPixel, bounds, width, height, analysis.alpha, cfg); ok {
				candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(&candidates, candidateCount, literalBestIndex, blockPredictorPlan, width, height, best, bestBits)
			}
		}

		for _, mode := range cfg.predictorModes {
			readResidual := vp8lPredictorResidualReader(readPixel, bounds, width, height, mode)
			residualAnalysis := analyzeImage(readResidual, bounds)
			candidate := vp8lEncodingPlan{
				analysis:          residualAnalysis,
				alpha:             analysis.alpha,
				predictor:         true,
				predictorMode:     mode,
				predictorSizeBits: vp8lDefaultPredictorSizeBits,
				predictorAnalysis: vp8lPredictorImageAnalysis(mode),
			}
			candidateBits := vp8lPayloadBits(width, height, candidate)
			candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(&candidates, candidateCount, literalBestIndex, candidate, width, height, best, bestBits)
			if !cfg.tryCombinedTransforms || !vp8lShouldTryCombinedTransform(candidateBits, bestBits) {
				continue
			}
			if vp8lShouldTrySubtractGreenAfterTransform(readResidual, bounds, width) {
				readPredictorSubtractGreen := vp8lSubtractGreenReader(readResidual)
				predictorSubtractGreenAnalysis := analyzeImage(readPredictorSubtractGreen, bounds)
				predictorSubtractGreenCandidate := candidate
				predictorSubtractGreenCandidate.analysis = predictorSubtractGreenAnalysis
				predictorSubtractGreenCandidate.subtractGreen = true
				candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(&candidates, candidateCount, literalBestIndex, predictorSubtractGreenCandidate, width, height, best, bestBits)
			}
			for _, element := range cfg.colorTransformCandidates {
				if !vp8lShouldTryColorTransformAfterTransform(readResidual, bounds, width, element) {
					continue
				}
				readPredictorColorTransform := vp8lColorTransformReader(readResidual, element)
				predictorColorTransformAnalysis := analyzeImage(readPredictorColorTransform, bounds)
				predictorColorTransformCandidate := candidate
				predictorColorTransformCandidate.analysis = predictorColorTransformAnalysis
				predictorColorTransformCandidate.colorTransform = true
				predictorColorTransformCandidate.colorSizeBits = vp8lDefaultColorTransformSizeBits
				predictorColorTransformCandidate.colorElement = element
				predictorColorTransformCandidate.colorAnalysis = vp8lColorTransformImageAnalysis(element)
				candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(&candidates, candidateCount, literalBestIndex, predictorColorTransformCandidate, width, height, best, bestBits)
			}
		}

		readSubtractGreen := vp8lSubtractGreenReader(readPixel)
		subtractGreenAnalysis := analyzeImage(readSubtractGreen, bounds)
		subtractGreenCandidate := vp8lEncodingPlan{
			analysis:      subtractGreenAnalysis,
			alpha:         analysis.alpha,
			subtractGreen: true,
		}
		candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(&candidates, candidateCount, literalBestIndex, subtractGreenCandidate, width, height, best, bestBits)

		for _, element := range cfg.colorTransformCandidates {
			readColorTransform := vp8lColorTransformReader(readPixel, element)
			colorAnalysis := analyzeImage(readColorTransform, bounds)
			candidate := vp8lEncodingPlan{
				analysis:       colorAnalysis,
				alpha:          analysis.alpha,
				colorTransform: true,
				colorSizeBits:  vp8lDefaultColorTransformSizeBits,
				colorElement:   element,
				colorAnalysis:  vp8lColorTransformImageAnalysis(element),
			}
			candidateBits := vp8lPayloadBits(width, height, candidate)
			candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(&candidates, candidateCount, literalBestIndex, candidate, width, height, best, bestBits)
			if !cfg.tryCombinedTransforms || !vp8lShouldTryCombinedTransform(candidateBits, bestBits) {
				continue
			}
			if !vp8lShouldTrySubtractGreenAfterTransform(readColorTransform, bounds, width) {
				continue
			}
			readColorTransformSubtractGreen := vp8lSubtractGreenReader(readColorTransform)
			colorTransformSubtractGreenAnalysis := analyzeImage(readColorTransformSubtractGreen, bounds)
			colorTransformSubtractGreenCandidate := candidate
			colorTransformSubtractGreenCandidate.analysis = colorTransformSubtractGreenAnalysis
			colorTransformSubtractGreenCandidate.subtractGreen = true
			candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(&candidates, candidateCount, literalBestIndex, colorTransformSubtractGreenCandidate, width, height, best, bestBits)
		}
	}

	if colorIndexOK {
		candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(&candidates, candidateCount, literalBestIndex, colorIndexPlan, width, height, best, bestBits)
	}
	if indexedPredictorOK {
		candidateCount, literalBestIndex, best, bestBits = vp8lAddEncodingPlanCandidate(&candidates, candidateCount, literalBestIndex, indexedPredictorPlan, width, height, best, bestBits)
	}

	return vp8lFinalizeEncodingPlan(readPixel, bounds, width, height, literalPlan, &candidates, candidateCount, literalBestIndex, best, bestBits, cfg)
}

func vp8lDefaultEncodingConfig() vp8lEncodingConfig {
	return vp8lEncodingConfig{
		allowColorIndexEarlyExit:        true,
		tryTransforms:                   true,
		tryCombinedTransforms:           true,
		tryLZ77:                         true,
		tryMetaPrefix:                   true,
		tryColorCache:                   true,
		tryLZ77ColorCache:               true,
		tryLZ77MetaPrefix:               true,
		tryTransformedLZ77ColorCache:    true,
		optimalLZ77Passes:               1,
		maxOptimalLZ77Pixels:            vp8lMaxOptimalLZ77Pixels,
		minColorIndexEarlyExitPixels:    vp8lMinColorIndexEarlyExitPixels,
		palettedColorIndexEarlyExitRate: 2,
		nrgbaColorIndexEarlyExitRate:    4,
		predictorModes:                  vp8lPredictorModeCandidates[:],
		predictorBlockSizeBits:          vp8lPredictorBlockSizeBitCandidates[:],
		colorTransformCandidates:        vp8lColorTransformCandidates[:],
		maxMetaPrefixLZ77Tokens:         vp8lMaxMetaPrefixLZ77Tokens,
		maxTransformedLZ77CacheTokens:   vp8lMaxTransformedLZ77CacheTokens,
	}
}

func vp8lEncodingConfigForMode(mode Mode, m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, height int) vp8lEncodingConfig {
	if mode == ModeAuto {
		mode, _ = vp8lAutoLosslessProfile(m, readPixel, bounds, width, height)
	}
	cfg := vp8lDefaultEncodingConfig()
	switch mode {
	case ModeFast:
		cfg.tryTransforms = false
		cfg.tryCombinedTransforms = false
		cfg.tryLZ77 = false
		cfg.tryMetaPrefix = false
		cfg.tryColorCache = false
		cfg.tryLZ77ColorCache = false
		cfg.tryLZ77MetaPrefix = false
		cfg.tryTransformedLZ77ColorCache = false
		cfg.optimalLZ77Passes = 0
	case ModeBestCompression:
		cfg.allowColorIndexEarlyExit = false
		cfg.predictorModes = vp8lBestCompressionPredictorModeCandidates[:]
		cfg.predictorBlockSizeBits = vp8lBestCompressionPredictorBlockSizeBitCandidates[:]
		cfg.colorTransformCandidates = vp8lBestCompressionColorTransformCandidates[:]
		cfg.tryBlockPredictor = true
		cfg.prioritizeColorIndexCandidate = true
		cfg.tryLZ77TokenMetaPrefix = true
		cfg.optimalLZ77Passes = 2
		cfg.maxOptimalLZ77Pixels = vp8lBestCompressionMaxOptimalLZ77Pixels
		cfg.maxMetaPrefixLZ77Tokens = vp8lBestCompressionMaxMetaPrefixLZ77Tokens
		cfg.maxTransformedLZ77CacheTokens = vp8lBestCompressionMaxTransformedLZ77CacheTokens
		cfg.parallelTransforms = true
	case ModeLowMemory:
		cfg.tryLZ77 = false
		cfg.tryMetaPrefix = false
		cfg.tryColorCache = false
		cfg.tryLZ77ColorCache = false
		cfg.tryLZ77MetaPrefix = false
		cfg.tryTransformedLZ77ColorCache = false
		cfg.optimalLZ77Passes = 0
	case ModeNearLossless:
		cfg.tryMetaPrefix = false
		cfg.tryLZ77MetaPrefix = false
		cfg.optimalLZ77Passes = 0
	}
	return cfg
}

func vp8lAutoLosslessMode(m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, height int) Mode {
	mode, _ := vp8lAutoLosslessProfile(m, readPixel, bounds, width, height)
	return mode
}

func vp8lAutoLosslessProfile(m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, height int) (Mode, vp8lAutoLosslessReason) {
	total := width * height
	sampledLowColor := total >= vp8lAutoMinColorIndexPixels && vp8lSampleUniqueColors(readPixel, bounds, width) <= 16
	verifiedLowColor := false
	if sampledLowColor {
		analysis := analyzeImage(readPixel, bounds)
		colorIndexPlan, ok := makeVP8LColorIndexingPlanForImage(m, readPixel, bounds, width, height, analysis.alpha)
		if ok && len(colorIndexPlan.colorTable) <= 16 {
			verifiedLowColor = true
			literalPlan := vp8lEncodingPlan{analysis: analysis, alpha: analysis.alpha}
			colorIndexBits := vp8lPayloadBits(width, height, colorIndexPlan)
			literalBits := vp8lPayloadBits(width, height, literalPlan)
			if colorIndexBits*4 <= literalBits && vp8lAutoFastColorIndexPayloadIsSmall(colorIndexBits, total) {
				return ModeFast, vp8lAutoLosslessReasonLargeLowColor
			}
		}
	}
	if total >= 4096*4096 {
		return ModeLowMemory, vp8lAutoLosslessReasonHugeImage
	}
	return ModeBalanced, vp8lAutoLosslessBalancedReason(m, readPixel, bounds, width, sampledLowColor, verifiedLowColor)
}

func vp8lAutoFastColorIndexPayloadIsSmall(colorIndexBits uint64, totalPixels int) bool {
	return colorIndexBits*vp8lAutoFastColorIndexMaxBitsPerPixelDenominator <= uint64(totalPixels)*vp8lAutoFastColorIndexMaxBitsPerPixelNumerator
}

func vp8lAutoLosslessBalancedReason(m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, sampledLowColor bool, verifiedLowColor bool) vp8lAutoLosslessReason {
	if sampledLowColor && !verifiedLowColor {
		return vp8lAutoLosslessReasonBalanced
	}
	if _, ok := m.(*image.Paletted); ok {
		return vp8lAutoLosslessReasonPaletteLike
	}
	stats := vp8lAutoSampleStatsFor(readPixel, bounds, width)
	if stats.samples == 0 {
		return vp8lAutoLosslessReasonBalanced
	}
	if stats.alphaNonOpaque*2 >= stats.samples {
		return vp8lAutoLosslessReasonAlphaHeavy
	}
	if stats.unique <= 1 {
		return vp8lAutoLosslessReasonFlat
	}
	if verifiedLowColor && stats.unique <= 16 {
		return vp8lAutoLosslessReasonUILike
	}
	if stats.adjacentCount > 0 && stats.adjacentDelta <= uint64(stats.adjacentCount*vp8lAutoGradientMaxAdjacentDelta) {
		return vp8lAutoLosslessReasonGradientLike
	}
	if stats.unique >= vp8lAutoPhotoLikeMinSampleColors {
		return vp8lAutoLosslessReasonPhotoLike
	}
	return vp8lAutoLosslessReasonBalanced
}

type vp8lAutoSampleStats struct {
	samples        int
	unique         int
	alphaNonOpaque int
	adjacentDelta  uint64
	adjacentCount  int
}

func vp8lAutoSampleStatsFor(readPixel pixelReader, bounds image.Rectangle, width int) vp8lAutoSampleStats {
	total := bounds.Dx() * bounds.Dy()
	if total == 0 {
		return vp8lAutoSampleStats{}
	}
	step := 1
	if total > vp8lAutoMaxSamples {
		step = total / vp8lAutoMaxSamples
	}
	seen := make(map[color.NRGBA]struct{}, 64)
	var stats vp8lAutoSampleStats
	for pos := 0; pos < total && stats.samples < vp8lAutoMaxSamples; pos += step {
		x := bounds.Min.X + pos%width
		y := bounds.Min.Y + pos/width
		p := readPixel(x, y)
		if len(seen) < vp8lAutoSampleUniqueCap {
			seen[p] = struct{}{}
		}
		if p.A != 255 {
			stats.alphaNonOpaque++
		}
		if x+1 < bounds.Max.X {
			stats.adjacentDelta += uint64(nrgbaManhattanDistance(p, readPixel(x+1, y)))
			stats.adjacentCount++
		}
		if y+1 < bounds.Max.Y {
			stats.adjacentDelta += uint64(nrgbaManhattanDistance(p, readPixel(x, y+1)))
			stats.adjacentCount++
		}
		stats.samples++
	}
	stats.unique = len(seen)
	return stats
}

func vp8lSampleUniqueColors(readPixel pixelReader, bounds image.Rectangle, width int) int {
	total := bounds.Dx() * bounds.Dy()
	if total == 0 {
		return 0
	}
	step := 1
	if total > vp8lAutoMaxSamples {
		step = total / vp8lAutoMaxSamples
	}
	seen := make(map[color.NRGBA]struct{}, 32)
	samples := 0
	for pos := 0; pos < total && samples < vp8lAutoMaxSamples; pos += step {
		seen[vp8lPixelAt(readPixel, bounds, width, pos)] = struct{}{}
		if len(seen) > 16 {
			return len(seen)
		}
		samples++
	}
	return len(seen)
}

func vp8lShouldUsePalettedColorIndexEarlyExit(m image.Image, plan vp8lEncodingPlan, colorIndexBits uint64, literalBits uint64, width int, height int) bool {
	if _, ok := m.(*image.Paletted); !ok {
		return false
	}
	return vp8lShouldUseColorIndexEarlyExit(m, plan, colorIndexBits, literalBits, width, height)
}

func vp8lShouldUseColorIndexEarlyExit(m image.Image, plan vp8lEncodingPlan, colorIndexBits uint64, literalBits uint64, width int, height int) bool {
	return vp8lShouldUseColorIndexEarlyExitConfig(m, plan, colorIndexBits, literalBits, width, height, vp8lDefaultEncodingConfig())
}

func vp8lShouldUseColorIndexEarlyExitConfig(m image.Image, plan vp8lEncodingPlan, colorIndexBits uint64, literalBits uint64, width int, height int, cfg vp8lEncodingConfig) bool {
	if !plan.colorIndexing || plan.colorIndexReader == nil || len(plan.colorTable) > 16 {
		return false
	}
	if width*height < cfg.minColorIndexEarlyExitPixels {
		return false
	}
	switch m.(type) {
	case *image.Paletted:
		return colorIndexBits*cfg.palettedColorIndexEarlyExitRate <= literalBits
	case *image.NRGBA:
		return colorIndexBits*cfg.nrgbaColorIndexEarlyExitRate <= literalBits
	default:
		return false
	}
}

const (
	vp8lMinColorIndexEarlyExitPixels                 = 64 * 64
	vp8lAutoMinColorIndexPixels                      = 512 * 512
	vp8lAutoFastColorIndexMaxBitsPerPixelNumerator   = 1
	vp8lAutoFastColorIndexMaxBitsPerPixelDenominator = 4
	vp8lAutoMaxSamples                               = 2048
	vp8lAutoSampleUniqueCap                          = 257
	vp8lAutoGradientMaxAdjacentDelta                 = 48
	vp8lAutoPhotoLikeMinSampleColors                 = 128
)

func vp8lFinalizeEncodingPlan(readPixel pixelReader, bounds image.Rectangle, width int, height int, literalPlan vp8lEncodingPlan, candidates *[vp8lMaxEncodingPlanCandidates]vp8lEncodingPlan, candidateCount int, literalBestIndex int, best vp8lEncodingPlan, bestBits uint64, cfg vp8lEncodingConfig) vp8lEncodingPlan {
	literalBestUsesTransform := vp8lPlanUsesTransform(candidates[literalBestIndex])
	if cfg.tryLZ77 {
		for i := 0; i < candidateCount; i++ {
			candidate := candidates[i]
			if literalBestUsesTransform && !vp8lPlanUsesTransform(candidate) {
				continue
			}
			best, bestBits = vp8lConsiderCandidateLZ77Config(readPixel, bounds, width, height, candidate, best, bestBits, cfg)
		}
	}
	if cfg.tryMetaPrefix {
		for i := 0; i < candidateCount; i++ {
			best, bestBits = vp8lConsiderCandidateMetaPrefix(readPixel, bounds, width, height, candidates[i], best, bestBits)
		}
	}
	if cfg.tryColorCache && vp8lShouldTryDefaultColorCache(best, literalPlan) {
		if colorCachePlan, ok := makeVP8LColorCachePlanConfig(readPixel, bounds, width, height, literalPlan, bestBits, cfg); ok {
			best = colorCachePlan
			bestBits = vp8lPayloadBits(width, height, best)
		}
	}

	return best
}

func vp8lAddEncodingPlanCandidate(candidates *[vp8lMaxEncodingPlanCandidates]vp8lEncodingPlan, candidateCount int, literalBestIndex int, candidate vp8lEncodingPlan, width int, height int, best vp8lEncodingPlan, bestBits uint64) (int, int, vp8lEncodingPlan, uint64) {
	if candidateCount >= len(candidates) {
		return candidateCount, literalBestIndex, best, bestBits
	}
	candidateIndex := candidateCount
	candidates[candidateIndex] = candidate
	candidateCount++
	candidateBits := vp8lPayloadBits(width, height, candidate)
	if candidateBits < bestBits {
		best = candidate
		bestBits = candidateBits
		literalBestIndex = candidateIndex
	}
	return candidateCount, literalBestIndex, best, bestBits
}

func vp8lShouldTryCombinedTransform(candidateBits uint64, bestBits uint64) bool {
	return candidateBits <= bestBits*2
}

func vp8lShouldTrySubtractGreenAfterTransform(readTransformed pixelReader, bounds image.Rectangle, width int) bool {
	total := bounds.Dx() * bounds.Dy()
	if total < 16 {
		return false
	}
	step := 1
	if total > vp8lTransformComboSampleSize {
		step = total / vp8lTransformComboSampleSize
	}
	var rgSeen [256]bool
	var bgSeen [256]bool
	rgUnique := 0
	bgUnique := 0
	samples := 0
	for pos := 0; pos < total && samples < vp8lTransformComboSampleSize; pos += step {
		pixel := vp8lPixelAt(readTransformed, bounds, width, pos)
		rg := pixel.R - pixel.G
		if !rgSeen[rg] {
			rgSeen[rg] = true
			rgUnique++
		}
		bg := pixel.B - pixel.G
		if !bgSeen[bg] {
			bgSeen[bg] = true
			bgUnique++
		}
		if rgUnique > vp8lTransformComboMaxDeltaSymbols || bgUnique > vp8lTransformComboMaxDeltaSymbols {
			return false
		}
		samples++
	}
	return samples >= 16
}

func vp8lShouldTryColorTransformAfterTransform(readTransformed pixelReader, bounds image.Rectangle, width int, element vp8lColorTransformElement) bool {
	redAffected := element.greenToRed != 0
	blueAffected := element.greenToBlue != 0 || element.redToBlue != 0
	if !redAffected && !blueAffected {
		return false
	}
	total := bounds.Dx() * bounds.Dy()
	if total < 16 {
		return false
	}
	step := 1
	if total > vp8lTransformComboSampleSize {
		step = total / vp8lTransformComboSampleSize
	}
	var redBeforeSeen [256]bool
	var redAfterSeen [256]bool
	var blueBeforeSeen [256]bool
	var blueAfterSeen [256]bool
	redBeforeUnique := 0
	redAfterUnique := 0
	blueBeforeUnique := 0
	blueAfterUnique := 0
	samples := 0
	for pos := 0; pos < total && samples < vp8lTransformComboSampleSize; pos += step {
		pixel := vp8lPixelAt(readTransformed, bounds, width, pos)
		transformed := applyVP8LColorTransform(pixel, element)
		if redAffected {
			if !redBeforeSeen[pixel.R] {
				redBeforeSeen[pixel.R] = true
				redBeforeUnique++
			}
			if !redAfterSeen[transformed.R] {
				redAfterSeen[transformed.R] = true
				redAfterUnique++
			}
		}
		if blueAffected {
			if !blueBeforeSeen[pixel.B] {
				blueBeforeSeen[pixel.B] = true
				blueBeforeUnique++
			}
			if !blueAfterSeen[transformed.B] {
				blueAfterSeen[transformed.B] = true
				blueAfterUnique++
			}
		}
		samples++
	}
	if samples < 16 {
		return false
	}
	redImproves := redAffected && vp8lUniqueCountClearlyImproves(redBeforeUnique, redAfterUnique)
	blueImproves := blueAffected && vp8lUniqueCountClearlyImproves(blueBeforeUnique, blueAfterUnique)
	return redImproves || blueImproves
}

func vp8lUniqueCountClearlyImproves(before int, after int) bool {
	return before >= 16 && (after <= 8 || after*2 <= before)
}

func makeVP8LBlockPredictorPlan(readPixel pixelReader, bounds image.Rectangle, width int, height int, alpha bool, cfg vp8lEncodingConfig) (vp8lEncodingPlan, bool) {
	best := vp8lEncodingPlan{}
	bestBits := uint64(0)
	found := false
	for _, sizeBits := range cfg.predictorBlockSizeBits {
		transformWidth, transformHeight := vp8lTransformDimensions(width, height, sizeBits)
		if transformWidth*transformHeight < 2 {
			continue
		}
		predictorImage, uniform := vp8lChooseBlockPredictorImage(readPixel, bounds, width, height, sizeBits, cfg.predictorModes)
		if len(predictorImage) == 0 || uniform {
			continue
		}
		readResidual := vp8lBlockPredictorResidualReader(readPixel, bounds, width, height, sizeBits, predictorImage, transformWidth)
		residualAnalysis := analyzeImage(readResidual, bounds)
		predictorBounds := image.Rect(0, 0, transformWidth, transformHeight)
		candidate := vp8lEncodingPlan{
			analysis:          residualAnalysis,
			alpha:             alpha,
			predictor:         true,
			predictorMode:     predictorImage[0],
			predictorSizeBits: sizeBits,
			predictorImage:    predictorImage,
			predictorAnalysis: analyzeImage(vp8lPredictorImageReaderFromImage(predictorImage, transformWidth), predictorBounds),
		}
		candidateBits := vp8lPayloadBits(width, height, candidate)
		if !found || candidateBits < bestBits {
			best = candidate
			bestBits = candidateBits
			found = true
		}
	}
	return best, found
}

func vp8lChooseBlockPredictorImage(readPixel pixelReader, bounds image.Rectangle, width int, height int, sizeBits uint8, modes []uint8) ([]uint8, bool) {
	if len(modes) == 0 {
		return nil, true
	}
	transformWidth, transformHeight := vp8lTransformDimensions(width, height, sizeBits)
	image := make([]uint8, transformWidth*transformHeight)
	uniform := true
	firstMode := uint8(0)
	for by := 0; by < transformHeight; by++ {
		for bx := 0; bx < transformWidth; bx++ {
			mode := vp8lBestPredictorModeForBlock(readPixel, bounds, width, height, sizeBits, bx, by, modes)
			if bx == 0 && by == 0 {
				firstMode = mode
			} else if mode != firstMode {
				uniform = false
			}
			image[by*transformWidth+bx] = mode
		}
	}
	return image, uniform
}

func vp8lBestPredictorModeForBlock(readPixel pixelReader, bounds image.Rectangle, width int, height int, sizeBits uint8, blockX int, blockY int, modes []uint8) uint8 {
	x0 := bounds.Min.X + blockX*(1<<sizeBits)
	y0 := bounds.Min.Y + blockY*(1<<sizeBits)
	x1 := minInt(x0+(1<<sizeBits), bounds.Max.X)
	y1 := minInt(y0+(1<<sizeBits), bounds.Max.Y)
	bestMode := modes[0]
	bestScore := uint64(1<<64 - 1)
	for _, mode := range modes {
		score := vp8lPredictorBlockResidualScore(readPixel, bounds, width, height, x0, y0, x1, y1, mode)
		if score < bestScore {
			bestScore = score
			bestMode = mode
		}
	}
	return bestMode
}

func vp8lPredictorBlockResidualScore(readPixel pixelReader, bounds image.Rectangle, width int, height int, x0 int, y0 int, x1 int, y1 int, mode uint8) uint64 {
	var score uint64
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			c := readPixel(x, y)
			pred := vp8lPredictorPixel(readPixel, bounds, width, height, x, y, mode)
			residual := subtractNRGBA(c, pred)
			score += uint64(vp8lResidualMagnitude(residual.G))
			score += uint64(vp8lResidualMagnitude(residual.R))
			score += uint64(vp8lResidualMagnitude(residual.B))
			score += uint64(vp8lResidualMagnitude(residual.A))
		}
	}
	return score
}

func vp8lResidualMagnitude(v uint8) uint8 {
	if v <= 128 {
		return v
	}
	return uint8(256 - int(v))
}

func vp8lConsiderCandidateLZ77(readPixel pixelReader, bounds image.Rectangle, width int, height int, candidate vp8lEncodingPlan, best vp8lEncodingPlan, bestBits uint64) (vp8lEncodingPlan, uint64) {
	return vp8lConsiderCandidateLZ77Config(readPixel, bounds, width, height, candidate, best, bestBits, vp8lDefaultEncodingConfig())
}

func vp8lConsiderCandidateLZ77Config(readPixel pixelReader, bounds image.Rectangle, width int, height int, candidate vp8lEncodingPlan, best vp8lEncodingPlan, bestBits uint64, cfg vp8lEncodingConfig) (vp8lEncodingPlan, uint64) {
	candidateBits := vp8lPayloadBits(width, height, candidate)
	if !vp8lShouldTryCandidateLZ77(candidate, candidateBits, bestBits) {
		return best, bestBits
	}
	lz77Plan, ok := makeVP8LLZ77PlanConfig(readPixel, bounds, width, height, candidate, bestBits, cfg)
	if !ok {
		return best, bestBits
	}
	lz77Bits := vp8lPayloadBits(width, height, lz77Plan)
	if lz77Bits < bestBits {
		best = lz77Plan
		bestBits = lz77Bits
	}
	return best, bestBits
}

func vp8lConsiderCandidateMetaPrefix(readPixel pixelReader, bounds image.Rectangle, width int, height int, candidate vp8lEncodingPlan, best vp8lEncodingPlan, bestBits uint64) (vp8lEncodingPlan, uint64) {
	candidateBits := vp8lPayloadBits(width, height, candidate)
	if !vp8lShouldTryCandidateMetaPrefix(candidate, candidateBits, bestBits) {
		return best, bestBits
	}
	metaPrefixPlan, ok := makeVP8LMetaPrefixPlan(readPixel, bounds, width, height, candidate, bestBits)
	if !ok {
		return best, bestBits
	}
	metaPrefixBits := vp8lPayloadBits(width, height, metaPrefixPlan)
	if metaPrefixBits < bestBits {
		best = metaPrefixPlan
		bestBits = metaPrefixBits
	}
	return best, bestBits
}

func vp8lPlanUsesTransform(plan vp8lEncodingPlan) bool {
	return plan.predictor || plan.colorTransform || plan.subtractGreen || plan.colorIndexing
}

func vp8lShouldTryDefaultColorCache(best vp8lEncodingPlan, literalPlan vp8lEncodingPlan) bool {
	return !best.colorIndexing && !literalPlan.analysis.allChannelsConstant()
}

func vp8lShouldTryCandidateLZ77(candidate vp8lEncodingPlan, candidateBits uint64, bestBits uint64) bool {
	if candidate.lz77 || candidate.colorCache != nil || candidate.analysis.allChannelsConstant() {
		return false
	}
	if candidateBits <= bestBits {
		return true
	}
	return candidateBits <= bestBits+bestBits/2+4096
}

func vp8lShouldTryCandidateMetaPrefix(candidate vp8lEncodingPlan, candidateBits uint64, bestBits uint64) bool {
	if candidate.metaPrefix != nil || candidate.lz77 || candidate.colorCache != nil || candidate.colorIndexing || vp8lPlanTransformCount(candidate) > 1 || candidate.analysis.allChannelsConstant() {
		return false
	}
	return candidateBits <= bestBits
}

func vp8lPlanTransformCount(plan vp8lEncodingPlan) int {
	count := 0
	if plan.predictor {
		count++
	}
	if plan.colorTransform {
		count++
	}
	if plan.subtractGreen {
		count++
	}
	if plan.colorIndexing {
		count++
	}
	return count
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
			a := pix[i+3]
			if a == 255 {
				return color.NRGBA{R: pix[i+0], G: pix[i+1], B: pix[i+2], A: 255}
			}
			return nrgbaFromRGBA(pix[i+0], pix[i+1], pix[i+2], a)
		}
	case *image.Gray:
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) color.NRGBA {
			gray := pix[(y-minY)*stride+x-minX]
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
			if pix[i+3] == 255 {
				return rgbToLuma(pix[i+0], pix[i+1], pix[i+2])
			}
			c := nrgbaFromRGBA(pix[i+0], pix[i+1], pix[i+2], pix[i+3])
			return rgbToLuma(c.R, c.G, c.B)
		}
	case *image.Gray:
		pix := img.Pix
		stride := img.Stride
		minX := img.Rect.Min.X
		minY := img.Rect.Min.Y
		return func(x int, y int) uint8 {
			value := pix[(y-minY)*stride+x-minX]
			return rgbToLuma(value, value, value)
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
			if pix[i+3] == 255 {
				return rgbToChroma(pix[i+0], pix[i+1], pix[i+2])
			}
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

const (
	vp8lDefaultPredictorSizeBits                     = 9
	vp8lDefaultColorTransformSizeBits                = 9
	vp8lTransformComboSampleSize                     = 512
	vp8lTransformComboMaxDeltaSymbols                = 8
	vp8lMaxEncodingPlanCandidates                    = 20
	vp8lMinTransformedLZ77CacheBits                  = 16 * 1024
	vp8lMaxTransformedLZ77CacheTokens                = 1 << 18
	vp8lMaxMetaPrefixLZ77Tokens                      = 1 << 13
	vp8lBestCompressionMaxTransformedLZ77CacheTokens = 1 << 19
	vp8lBestCompressionMaxMetaPrefixLZ77Tokens       = 1 << 15
	vp8lMaxOptimalLZ77Pixels                         = 1 << 18
	vp8lBestCompressionMaxOptimalLZ77Pixels          = 1 << 20
)

var vp8lPredictorModeCandidates = [...]uint8{1, 2, 12}
var vp8lBestCompressionPredictorModeCandidates = [...]uint8{1, 2, 12, 3, 5, 6, 7, 8, 9, 10, 11, 13}
var vp8lPredictorBlockSizeBitCandidates = [...]uint8{vp8lDefaultPredictorSizeBits}
var vp8lBestCompressionPredictorBlockSizeBitCandidates = [...]uint8{6, 7, 8, vp8lDefaultPredictorSizeBits}
var vp8lColorTransformCandidates = [...]vp8lColorTransformElement{
	{greenToRed: 32},
	{greenToBlue: 32},
	{redToBlue: 32},
	{greenToRed: 32, greenToBlue: 32},
}
var vp8lBestCompressionColorTransformCandidates = [...]vp8lColorTransformElement{
	{greenToRed: 16},
	{greenToRed: 32},
	{greenToRed: 64},
	{greenToBlue: 16},
	{greenToBlue: 32},
	{greenToBlue: 64},
	{redToBlue: 32},
	{greenToRed: 32, greenToBlue: 32},
	{greenToRed: 64, greenToBlue: 64},
	{greenToBlue: 32, redToBlue: 32},
}

func vp8lPayloadBits(width int, height int, plan vp8lEncodingPlan) uint64 {
	return vp8lPayloadBitBreakdownFor(width, height, plan).total()
}

type vp8lPayloadBitBreakdown struct {
	fileHeader          uint64
	predictorHeader     uint64
	predictorImageData  uint64
	colorHeader         uint64
	colorImageData      uint64
	subtractGreenHeader uint64
	colorIndexHeader    uint64
	colorIndexImageData uint64
	transformTerminator uint64
	mainImageData       uint64
}

func (b vp8lPayloadBitBreakdown) total() uint64 {
	return b.fileHeader +
		b.predictorHeader + b.predictorImageData +
		b.colorHeader + b.colorImageData +
		b.subtractGreenHeader +
		b.colorIndexHeader + b.colorIndexImageData +
		b.transformTerminator +
		b.mainImageData
}

func (b vp8lPayloadBitBreakdown) transformHeaderBits() uint64 {
	return b.predictorHeader + b.colorHeader + b.subtractGreenHeader + b.colorIndexHeader + b.transformTerminator
}

func (b vp8lPayloadBitBreakdown) transformImageDataBits() uint64 {
	return b.predictorImageData + b.colorImageData + b.colorIndexImageData
}

func vp8lPayloadBitBreakdownFor(width int, height int, plan vp8lEncodingPlan) vp8lPayloadBitBreakdown {
	b := vp8lPayloadBitBreakdown{
		fileHeader:          8 + 14 + 14 + 4, // signature, dimensions, alpha hint, version
		transformTerminator: 1,
	}
	if plan.predictor {
		b.predictorHeader = 1 + 2 + 3 // transform present, predictor transform type, block size bits
		predictorWidth, predictorHeight := width, height
		if plan.colorIndexing {
			predictorWidth, predictorHeight = vp8lPlanImageDimensions(width, height, plan)
		}
		transformWidth, transformHeight := vp8lTransformDimensions(predictorWidth, predictorHeight, plan.predictorSizeBits)
		b.predictorImageData = vp8lImageDataBits(transformWidth, transformHeight, plan.predictorAnalysis, false)
	}
	if plan.colorTransform {
		b.colorHeader = 1 + 2 + 3 // transform present, color transform type, block size bits
		transformWidth, transformHeight := vp8lTransformDimensions(width, height, plan.colorSizeBits)
		b.colorImageData = vp8lImageDataBits(transformWidth, transformHeight, plan.colorAnalysis, false)
	}
	if plan.subtractGreen {
		b.subtractGreenHeader = 1 + 2 // transform present, subtract green transform type
	}
	if plan.colorIndexing {
		b.colorIndexHeader = 1 + 2 + 8 // transform present, color indexing transform type, color table size
		tableBounds := image.Rect(0, 0, len(plan.colorTable), 1)
		b.colorIndexImageData = vp8lImageDataBits(tableBounds.Dx(), tableBounds.Dy(), plan.colorIndexAnalysis, false)
	}
	mainWidth, mainHeight := vp8lPlanImageDimensions(width, height, plan)
	if plan.metaPrefix != nil {
		if plan.colorCache != nil {
			b.mainImageData = vp8lMetaPrefixColorCacheImageDataBits(plan)
		} else if plan.lz77 {
			b.mainImageData = vp8lMetaPrefixLZ77ImageDataBits(plan)
		} else {
			b.mainImageData = vp8lMetaPrefixImageDataBits(plan)
		}
	} else if plan.colorCache != nil {
		b.mainImageData = vp8lColorCacheImageDataBits(plan, true)
	} else if plan.lz77 {
		b.mainImageData = vp8lLZ77ImageDataBits(plan, true)
	} else {
		b.mainImageData = vp8lImageDataBits(mainWidth, mainHeight, plan.analysis, true)
	}
	return b
}

func vp8lMetaPrefixImageDataBits(plan vp8lEncodingPlan) uint64 {
	metaPrefix := plan.metaPrefix
	bits := uint64(0)
	bits += 1     // no color cache
	bits += 1 + 3 // meta prefix image present, prefix bits
	bits += vp8lImageDataBits(metaPrefix.width, metaPrefix.height, metaPrefix.imageAnalysis, false)
	for i, group := range metaPrefix.groups {
		bits += imageAnalysisTreeAndDataBits(group, metaPrefix.groupPixels[i])
	}
	return bits
}

func vp8lMetaPrefixColorCacheImageDataBits(plan vp8lEncodingPlan) uint64 {
	metaPrefix := plan.metaPrefix
	colorCache := plan.colorCache
	greenLimit := nLiteralCodes + nLengthCodes + 1<<colorCache.bits
	bits := uint64(0)
	bits += 1 + 4 // color cache present, color cache bits
	bits += 1 + 3 // meta prefix image present, prefix bits
	bits += vp8lImageDataBits(metaPrefix.width, metaPrefix.height, metaPrefix.imageAnalysis, false)
	for _, group := range metaPrefix.colorCacheGroups {
		bits += alphaNormalTreeBits(group.lengths[:greenLimit])
		literalCount := int(vp8lColorCacheGroupLiteralTokenCount(group))
		bits += channelTreeAndDataBits(group.literalAnalysis.channels[1], nLiteralCodes, literalCount)
		bits += channelTreeAndDataBits(group.literalAnalysis.channels[2], nLiteralCodes, literalCount)
		bits += channelTreeAndDataBits(group.literalAnalysis.channels[3], nLiteralCodes, literalCount)
		bits += simpleTreeBits(0)
		for symbol, count := range group.counts[:greenLimit] {
			if count == 0 {
				continue
			}
			bits += uint64(count) * uint64(group.lengths[symbol])
		}
	}
	return bits
}

func vp8lColorCacheGroupLiteralTokenCount(group vp8lColorCacheGroupPlan) uint64 {
	var total uint64
	for _, count := range group.counts[:nLiteralCodes] {
		total += uint64(count)
	}
	return total
}

func vp8lMetaPrefixLZ77ImageDataBits(plan vp8lEncodingPlan) uint64 {
	metaPrefix := plan.metaPrefix
	bits := uint64(0)
	bits += 1     // no color cache
	bits += 1 + 3 // meta prefix image present, prefix bits
	bits += vp8lImageDataBits(metaPrefix.width, metaPrefix.height, metaPrefix.imageAnalysis, false)
	for _, group := range metaPrefix.lz77Groups {
		bits += vp8lLZ77GroupTreeAndDataBits(group)
	}
	return bits
}

func vp8lLZ77GroupTreeAndDataBits(group vp8lLZ77GroupPlan) uint64 {
	bits := alphaNormalTreeBits(group.greenLengths[:])
	literalCount := int(vp8lLiteralTokenCount(group.greenCounts))
	bits += channelTreeAndDataBits(group.literalAnalysis.channels[1], nLiteralCodes, literalCount)
	bits += channelTreeAndDataBits(group.literalAnalysis.channels[2], nLiteralCodes, literalCount)
	bits += channelTreeAndDataBits(group.literalAnalysis.channels[3], nLiteralCodes, literalCount)
	bits += vp8lLZ77GroupDistanceTreeBits(group)

	for symbol, count := range group.greenCounts {
		if count == 0 {
			continue
		}
		bits += uint64(count) * uint64(group.greenLengths[symbol])
		if symbol >= nLiteralCodes {
			bits += uint64(count) * uint64(vp8lLengthPrefixExtraBits(symbol-nLiteralCodes))
		}
	}
	bits += vp8lLZ77GroupDistanceDataBits(group)
	return bits
}

func vp8lImageDataBits(width int, height int, analysis imageAnalysis, metaPrefix bool) uint64 {
	bits := uint64(0)
	bits += 1 // no color cache
	if metaPrefix {
		bits += 1 // no meta prefix image
	}

	return bits + imageAnalysisTreeAndDataBits(analysis, width*height)
}

func vp8lColorCacheImageDataBits(plan vp8lEncodingPlan, metaPrefix bool) uint64 {
	colorCache := plan.colorCache
	bits := uint64(0)
	bits += 1 + 4 // color cache present, color cache bits
	if metaPrefix {
		bits += 1 // no meta prefix image
	}

	greenLimit := nLiteralCodes + nLengthCodes + 1<<colorCache.bits
	bits += alphaNormalTreeBits(colorCache.lengths[:greenLimit])
	literalCount := int(vp8lColorCacheLiteralTokenCount(plan))
	bits += channelTreeAndDataBits(colorCache.analysis.channels[1], nLiteralCodes, literalCount)
	bits += channelTreeAndDataBits(colorCache.analysis.channels[2], nLiteralCodes, literalCount)
	bits += channelTreeAndDataBits(colorCache.analysis.channels[3], nLiteralCodes, literalCount)
	if plan.lz77 {
		bits += vp8lDistanceTreeBits(plan)
	} else {
		bits += simpleTreeBits(0)
	}

	for symbol, count := range colorCache.counts[:greenLimit] {
		if count == 0 {
			continue
		}
		bits += uint64(count) * uint64(colorCache.lengths[symbol])
		if symbol >= nLiteralCodes && symbol < nLiteralCodes+nLengthCodes {
			bits += uint64(count) * uint64(vp8lLengthPrefixExtraBits(symbol-nLiteralCodes))
		}
	}
	if plan.lz77 {
		bits += vp8lDistanceDataBits(plan)
	}
	return bits
}

func vp8lColorCacheLiteralTokenCount(plan vp8lEncodingPlan) uint64 {
	var total uint64
	for _, count := range plan.colorCache.counts[:nLiteralCodes] {
		total += uint64(count)
	}
	return total
}

func vp8lLZ77ImageDataBits(plan vp8lEncodingPlan, metaPrefix bool) uint64 {
	bits := uint64(0)
	bits += 1 // no color cache
	if metaPrefix {
		bits += 1 // no meta prefix image
	}

	bits += alphaNormalTreeBits(plan.lz77GreenLengths[:])
	literalCount := int(vp8lLiteralTokenCount(plan.lz77GreenCounts))
	bits += channelTreeAndDataBits(plan.lz77LiteralAnalysis.channels[1], nLiteralCodes, literalCount)
	bits += channelTreeAndDataBits(plan.lz77LiteralAnalysis.channels[2], nLiteralCodes, literalCount)
	bits += channelTreeAndDataBits(plan.lz77LiteralAnalysis.channels[3], nLiteralCodes, literalCount)
	bits += vp8lDistanceTreeBits(plan)

	for symbol, count := range plan.lz77GreenCounts {
		if count == 0 {
			continue
		}
		bits += uint64(count) * uint64(plan.lz77GreenLengths[symbol])
		if symbol >= nLiteralCodes {
			bits += uint64(count) * uint64(vp8lLengthPrefixExtraBits(symbol-nLiteralCodes))
		}
	}
	bits += vp8lDistanceDataBits(plan)
	return bits
}

func vp8lDistanceTreeBits(plan vp8lEncodingPlan) uint64 {
	if plan.lz77DistanceNormal {
		return alphaNormalTreeBits(plan.lz77DistanceLengths[:])
	}
	switch plan.lz77DistanceN {
	case 1:
		return simpleTreeBits(plan.lz77DistanceSymbols[0])
	case 2:
		return alphaTwoSymbolTreeBits(plan.lz77DistanceSymbols[0])
	default:
		return simpleTreeBits(0)
	}
}

func vp8lLZ77GroupDistanceTreeBits(group vp8lLZ77GroupPlan) uint64 {
	if group.distanceNormal {
		return alphaNormalTreeBits(group.distanceLengths[:])
	}
	switch group.distanceN {
	case 1:
		return simpleTreeBits(group.distanceSymbols[0])
	case 2:
		return alphaTwoSymbolTreeBits(group.distanceSymbols[0])
	default:
		return simpleTreeBits(0)
	}
}

func vp8lDistanceDataBits(plan vp8lEncodingPlan) uint64 {
	var bits uint64
	for symbol, count := range plan.lz77DistanceCounts {
		if count == 0 {
			continue
		}
		if plan.lz77DistanceNormal {
			bits += uint64(count) * uint64(plan.lz77DistanceLengths[symbol])
		} else if plan.lz77DistanceN == 2 {
			bits += uint64(count)
		}
		bits += uint64(count) * uint64(vp8lPrefixExtraBits(symbol))
	}
	return bits
}

func vp8lLZ77GroupDistanceDataBits(group vp8lLZ77GroupPlan) uint64 {
	var bits uint64
	for symbol, count := range group.distanceCounts {
		if count == 0 {
			continue
		}
		if group.distanceNormal {
			bits += uint64(count) * uint64(group.distanceLengths[symbol])
		} else if group.distanceN == 2 {
			bits += uint64(count)
		}
		bits += uint64(count) * uint64(vp8lPrefixExtraBits(symbol))
	}
	return bits
}

func vp8lLiteralTokenCount(greenCounts [nLiteralCodes + nLengthCodes]uint32) uint64 {
	var total uint64
	for _, count := range greenCounts[:nLiteralCodes] {
		total += uint64(count)
	}
	return total
}

func imageAnalysisTreeAndDataBits(analysis imageAnalysis, pixels int) uint64 {
	bits := channelTreeAndDataBits(analysis.channels[0], nLiteralCodes+nLengthCodes, pixels)
	bits += channelTreeAndDataBits(analysis.channels[1], nLiteralCodes, pixels)
	bits += channelTreeAndDataBits(analysis.channels[2], nLiteralCodes, pixels)
	bits += channelTreeAndDataBits(analysis.channels[3], nLiteralCodes, pixels)
	bits += simpleTreeBits(0)
	return bits
}

func channelTreeAndDataBits(ch channelPlan, alphabetSize int, pixels int) uint64 {
	if ch.constant {
		return simpleTreeBits(ch.value)
	}
	if ch.twoSymbol() {
		return alphaTwoSymbolTreeBits(ch.symbols[0]) + uint64(pixels)
	}
	if ch.normal {
		normalBits := channelNormalTreeBits(ch, alphabetSize) + channelNormalDataBits(ch)
		fullBits := full8TreeBits(alphabetSize) + channelFull8DataBits(ch)
		if normalBits < fullBits {
			return normalBits
		}
		return fullBits
	}
	return full8TreeBits(alphabetSize) + uint64(pixels)*8
}

func channelDataBits(ch channelPlan, alphabetSize int, pixels int) uint64 {
	switch {
	case ch.constant:
		return 0
	case ch.twoSymbol():
		return uint64(pixels)
	case ch.normal && channelUseNormal(ch, alphabetSize):
		return channelNormalDataBits(ch)
	default:
		return uint64(pixels) * 8
	}
}

func huffmanTreeBits(ch channelPlan, alphabetSize int) uint64 {
	if ch.constant {
		return simpleTreeBits(ch.value)
	}
	if ch.twoSymbol() {
		return alphaTwoSymbolTreeBits(ch.symbols[0])
	}
	if ch.normal && channelUseNormal(ch, alphabetSize) {
		return channelNormalTreeBits(ch, alphabetSize)
	}
	return full8TreeBits(alphabetSize)
}

func channelUseNormal(ch channelPlan, alphabetSize int) bool {
	if !ch.normal {
		return false
	}
	normalBits := channelNormalTreeBits(ch, alphabetSize) + channelNormalDataBits(ch)
	fullBits := full8TreeBits(alphabetSize) + channelFull8DataBits(ch)
	return normalBits < fullBits
}

func channelNormalTreeBits(ch channelPlan, alphabetSize int) uint64 {
	if ch.normalCostCached {
		return uint64(ch.normalTreeBaseBits) + alphaCodeLengthLimitBits(int(ch.normalTreeTokenCount), alphabetSize)
	}
	var lengths [nColorCacheGreenCodes]uint8
	for i := 0; i < ch.n; i++ {
		lengths[ch.symbols[i]] = ch.lengths[i]
	}
	return alphaNormalTreeBits(lengths[:alphabetSize])
}

func channelNormalDataBits(ch channelPlan) uint64 {
	if ch.normalCostCached {
		return ch.normalDataBits
	}
	var bits uint64
	for i := 0; i < ch.n; i++ {
		bits += uint64(ch.counts[i]) * uint64(ch.lengths[i])
	}
	return bits
}

func channelFull8DataBits(ch channelPlan) uint64 {
	var total uint64
	for i := 0; i < ch.n; i++ {
		total += uint64(ch.counts[i])
	}
	return total * 8
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

func writeVP8L(bits *bitWriter, readPixel pixelReader, bounds image.Rectangle, width int, height int, plan vp8lEncodingPlan) {
	writeVP8LHeader(bits, width, height, plan.alpha)
	if plan.colorIndexing && plan.predictor {
		writeVP8LColorIndexTransform(bits, plan)
		predictorWidth, predictorHeight := vp8lPlanImageDimensions(width, height, plan)
		writeVP8LPredictorTransform(bits, predictorWidth, predictorHeight, plan)
	} else if plan.predictor {
		writeVP8LPredictorTransform(bits, width, height, plan)
	}
	if plan.colorTransform {
		bits.writeBits(1, 1)
		bits.writeBits(1, 2)
		bits.writeBits(uint32(plan.colorSizeBits-2), 3)
		transformWidth, transformHeight := vp8lTransformDimensions(width, height, plan.colorSizeBits)
		transformBounds := image.Rect(0, 0, transformWidth, transformHeight)
		writeVP8LImageData(bits, vp8lColorTransformImageReader(plan.colorElement), transformBounds, plan.colorAnalysis, false)
	}
	if plan.subtractGreen {
		bits.writeBits(1, 1)
		bits.writeBits(2, 2)
	}
	if plan.colorIndexing && !plan.predictor {
		writeVP8LColorIndexTransform(bits, plan)
	}
	bits.writeBits(0, 1)
	mainWidth, mainHeight := vp8lPlanImageDimensions(width, height, plan)
	mainBounds := image.Rect(0, 0, mainWidth, mainHeight)
	if !plan.colorIndexing {
		mainBounds = bounds
	}
	if plan.metaPrefix != nil {
		if plan.colorCache != nil {
			writeVP8LMetaPrefixColorCacheImageData(bits, mainWidth, plan)
		} else if plan.lz77 {
			writeVP8LMetaPrefixLZ77ImageData(bits, vp8lPlanPixelReader(readPixel, bounds, width, height, plan), mainBounds, mainWidth, plan)
		} else {
			writeVP8LMetaPrefixImageData(bits, vp8lPlanPixelReader(readPixel, bounds, width, height, plan), mainBounds, plan)
		}
	} else if plan.colorCache != nil {
		writeVP8LColorCacheImageData(bits, plan, true)
	} else if plan.lz77 {
		writeVP8LLZ77ImageData(bits, vp8lPlanPixelReader(readPixel, bounds, width, height, plan), mainBounds, mainWidth, plan, true)
	} else {
		writeVP8LImageData(bits, vp8lPlanPixelReader(readPixel, bounds, width, height, plan), mainBounds, plan.analysis, true)
	}
}

func writeVP8LPredictorTransform(bits *bitWriter, width int, height int, plan vp8lEncodingPlan) {
	bits.writeBits(1, 1)
	bits.writeBits(0, 2)
	bits.writeBits(uint32(plan.predictorSizeBits-2), 3)
	transformWidth, transformHeight := vp8lTransformDimensions(width, height, plan.predictorSizeBits)
	transformBounds := image.Rect(0, 0, transformWidth, transformHeight)
	writeVP8LImageData(bits, vp8lPredictorImageReaderForPlan(plan, transformWidth), transformBounds, plan.predictorAnalysis, false)
}

func writeVP8LColorIndexTransform(bits *bitWriter, plan vp8lEncodingPlan) {
	bits.writeBits(1, 1)
	bits.writeBits(3, 2)
	bits.writeBits(uint32(len(plan.colorTable)-1), 8)
	tableBounds := image.Rect(0, 0, len(plan.colorTable), 1)
	writeVP8LImageData(bits, vp8lColorTableImageReader(plan.colorTable), tableBounds, plan.colorIndexAnalysis, false)
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

func vp8lPredictorImageAnalysis(mode uint8) imageAnalysis {
	return imageAnalysis{
		channels: [4]channelPlan{
			newConstantChannelPlan(mode),
			newConstantChannelPlan(0),
			newConstantChannelPlan(0),
			newConstantChannelPlan(255),
		},
	}
}

func vp8lPredictorImageReader(mode uint8) pixelReader {
	return func(int, int) color.NRGBA {
		return color.NRGBA{G: mode, A: 255}
	}
}

func vp8lPredictorImageReaderForPlan(plan vp8lEncodingPlan, width int) pixelReader {
	if len(plan.predictorImage) == 0 {
		return vp8lPredictorImageReader(plan.predictorMode)
	}
	return vp8lPredictorImageReaderFromImage(plan.predictorImage, width)
}

func vp8lPredictorImageReaderFromImage(image []uint8, width int) pixelReader {
	return func(x int, y int) color.NRGBA {
		return color.NRGBA{G: image[y*width+x], A: 255}
	}
}

func vp8lColorTransformImageAnalysis(element vp8lColorTransformElement) imageAnalysis {
	return imageAnalysis{
		channels: [4]channelPlan{
			newConstantChannelPlan(element.greenToBlue),
			newConstantChannelPlan(element.redToBlue),
			newConstantChannelPlan(element.greenToRed),
			newConstantChannelPlan(255),
		},
	}
}

func vp8lColorTransformImageReader(element vp8lColorTransformElement) pixelReader {
	return func(int, int) color.NRGBA {
		return color.NRGBA{
			R: element.redToBlue,
			G: element.greenToBlue,
			B: element.greenToRed,
			A: 255,
		}
	}
}

func makeVP8LColorIndexingPlan(readPixel pixelReader, bounds image.Rectangle, width int, height int, alpha bool) (vp8lEncodingPlan, bool) {
	table, index, ok := buildVP8LColorTable(readPixel, bounds)
	if !ok {
		return vp8lEncodingPlan{}, false
	}
	if len(table) == 0 || len(table) > 256 {
		return vp8lEncodingPlan{}, false
	}
	tableBounds := image.Rect(0, 0, len(table), 1)
	tableAnalysis := analyzeImage(vp8lColorTableImageReader(table), tableBounds)
	widthBits := vp8lColorIndexWidthBits(len(table))
	mainWidth := divRoundUp(width, 1<<widthBits)
	mainBounds := image.Rect(0, 0, mainWidth, height)
	readIndexed := vp8lColorIndexedImageReader(readPixel, bounds, width, widthBits, index)
	if len(table) <= 16 && mainWidth*height <= vp8lMaxMaterializedIndexPixels {
		readIndexed = vp8lMaterializedColorIndexReader(readIndexed, mainBounds)
	}
	best := vp8lEncodingPlan{
		analysis:            analyzeVP8LColorIndexedImage(readIndexed, mainBounds),
		alpha:               alpha,
		colorIndexing:       true,
		colorIndexWidthBits: widthBits,
		colorTable:          table,
		colorIndex:          index,
		colorIndexReader:    readIndexed,
		colorIndexAnalysis:  tableAnalysis,
	}
	if len(table) <= 16 {
		return best, true
	}
	bestBits := vp8lPayloadBits(width, height, best)
	sortedTable := vp8lSortedColorTable(table)
	if vp8lColorTablesEqual(table, sortedTable) {
		return best, true
	}
	sortedTableAnalysis := analyzeImage(vp8lColorTableImageReader(sortedTable), tableBounds)
	if vp8lImageDataBits(len(sortedTable), 1, sortedTableAnalysis, false) < vp8lImageDataBits(len(table), 1, tableAnalysis, false) {
		sortedPlan := makeVP8LColorIndexingPlanForTable(readPixel, bounds, width, height, alpha, sortedTable, sortedTableAnalysis)
		if sortedBits := vp8lPayloadBits(width, height, sortedPlan); sortedBits < bestBits {
			best = sortedPlan
		}
	}
	return best, true
}

func makeVP8LColorIndexingPlanForImage(m image.Image, readPixel pixelReader, bounds image.Rectangle, width int, height int, alpha bool) (vp8lEncodingPlan, bool) {
	if plan, ok := makeVP8LPalettedColorIndexingPlan(m, bounds, width, height, alpha); ok {
		return plan, true
	}
	plan, ok := makeVP8LColorIndexingPlan(readPixel, bounds, width, height, alpha)
	if !ok {
		return vp8lEncodingPlan{}, false
	}
	if readIndexed, ok := vp8lPalettedColorIndexedImageReader(m, bounds, width, plan.colorIndexWidthBits, plan.colorIndex); ok {
		plan.colorIndexReader = readIndexed
	}
	return plan, true
}

func makeVP8LPalettedColorIndexingPlan(m image.Image, bounds image.Rectangle, width int, height int, alpha bool) (vp8lEncodingPlan, bool) {
	table, index, ok := buildVP8LPalettedColorTable(m, bounds)
	if !ok {
		return vp8lEncodingPlan{}, false
	}
	if len(table) == 0 || len(table) > 256 {
		return vp8lEncodingPlan{}, false
	}
	tableBounds := image.Rect(0, 0, len(table), 1)
	tableAnalysis := analyzeImage(vp8lColorTableImageReader(table), tableBounds)
	widthBits := vp8lColorIndexWidthBits(len(table))
	readIndexed, ok := vp8lPalettedColorIndexedImageReader(m, bounds, width, widthBits, index)
	if !ok {
		return vp8lEncodingPlan{}, false
	}
	best := makeVP8LColorIndexingPlanForIndexedReader(width, height, alpha, table, index, tableAnalysis, readIndexed)
	best.colorIndexReader = readIndexed
	if len(table) <= 16 {
		return best, true
	}
	bestBits := vp8lPayloadBits(width, height, best)
	sortedTable := vp8lSortedColorTable(table)
	if vp8lColorTablesEqual(table, sortedTable) {
		return best, true
	}
	sortedTableAnalysis := analyzeImage(vp8lColorTableImageReader(sortedTable), tableBounds)
	if vp8lImageDataBits(len(sortedTable), 1, sortedTableAnalysis, false) >= vp8lImageDataBits(len(table), 1, tableAnalysis, false) {
		return best, true
	}
	sortedIndex := vp8lColorTableIndex(sortedTable)
	sortedReadIndexed, ok := vp8lPalettedColorIndexedImageReader(m, bounds, width, vp8lColorIndexWidthBits(len(sortedTable)), sortedIndex)
	if !ok {
		return best, true
	}
	sortedPlan := makeVP8LColorIndexingPlanForIndexedReader(width, height, alpha, sortedTable, sortedIndex, sortedTableAnalysis, sortedReadIndexed)
	sortedPlan.colorIndexReader = sortedReadIndexed
	if sortedBits := vp8lPayloadBits(width, height, sortedPlan); sortedBits < bestBits {
		best = sortedPlan
	}
	return best, true
}

func makeVP8LColorIndexingPlanForTable(readPixel pixelReader, bounds image.Rectangle, width int, height int, alpha bool, table []color.NRGBA, tableAnalysis imageAnalysis) vp8lEncodingPlan {
	return makeVP8LColorIndexingPlanForTableAndIndex(readPixel, bounds, width, height, alpha, table, vp8lColorTableIndex(table), tableAnalysis)
}

func makeVP8LColorIndexingPlanForTableAndIndex(readPixel pixelReader, bounds image.Rectangle, width int, height int, alpha bool, table []color.NRGBA, index map[color.NRGBA]uint8, tableAnalysis imageAnalysis) vp8lEncodingPlan {
	widthBits := vp8lColorIndexWidthBits(len(table))
	readIndexed := vp8lColorIndexedImageReader(readPixel, bounds, width, widthBits, index)
	return makeVP8LColorIndexingPlanForIndexedReader(width, height, alpha, table, index, tableAnalysis, readIndexed)
}

func makeVP8LColorIndexingPlanForIndexedReader(width int, height int, alpha bool, table []color.NRGBA, index map[color.NRGBA]uint8, tableAnalysis imageAnalysis, readIndexed pixelReader) vp8lEncodingPlan {
	widthBits := vp8lColorIndexWidthBits(len(table))
	mainWidth := divRoundUp(width, 1<<widthBits)
	mainBounds := image.Rect(0, 0, mainWidth, height)
	return vp8lEncodingPlan{
		analysis:            analyzeVP8LColorIndexedImage(readIndexed, mainBounds),
		alpha:               alpha,
		colorIndexing:       true,
		colorIndexWidthBits: widthBits,
		colorTable:          table,
		colorIndex:          index,
		colorIndexReader:    readIndexed,
		colorIndexAnalysis:  tableAnalysis,
	}
}

func vp8lMaterializedColorIndexReader(readPixel pixelReader, bounds image.Rectangle) pixelReader {
	width := bounds.Dx()
	pixels := make([]uint8, width*bounds.Dy())
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			pixels[(y-bounds.Min.Y)*width+x-bounds.Min.X] = readPixel(x, y).G
		}
	}
	return func(x int, y int) color.NRGBA {
		return color.NRGBA{
			G: pixels[(y-bounds.Min.Y)*width+x-bounds.Min.X],
			A: 255,
		}
	}
}

func analyzeVP8LColorIndexedImage(readPixel pixelReader, bounds image.Rectangle) imageAnalysis {
	var green channelPlan
	first := true
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			v := readPixel(x, y).G
			if first {
				green = newConstantChannelPlan(v)
				first = false
				continue
			}
			green.observe(v)
		}
	}
	if first {
		green = newConstantChannelPlan(0)
	} else {
		green.finalize()
	}
	return imageAnalysis{
		channels: [4]channelPlan{
			green,
			newConstantChannelPlan(0),
			newConstantChannelPlan(0),
			newConstantChannelPlan(255),
		},
	}
}

func buildVP8LColorTable(readPixel pixelReader, bounds image.Rectangle) ([]color.NRGBA, map[color.NRGBA]uint8, bool) {
	var table []color.NRGBA
	index := make(map[color.NRGBA]uint8)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := readPixel(x, y)
			if _, ok := index[c]; ok {
				continue
			}
			if len(table) == 256 {
				return nil, nil, false
			}
			index[c] = uint8(len(table))
			table = append(table, c)
		}
	}
	return table, index, true
}

func buildVP8LPalettedColorTable(m image.Image, bounds image.Rectangle) ([]color.NRGBA, map[color.NRGBA]uint8, bool) {
	img, ok := m.(*image.Paletted)
	if !ok || len(img.Palette) == 0 {
		return nil, nil, false
	}
	var palette [256]color.NRGBA
	var paletteValid [256]bool
	var table []color.NRGBA
	index := make(map[color.NRGBA]uint8)
	pix := img.Pix
	stride := img.Stride
	minX := img.Rect.Min.X
	minY := img.Rect.Min.Y
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		row := (y-minY)*stride + bounds.Min.X - minX
		for x := 0; x < bounds.Dx(); x++ {
			paletteIndex := pix[row+x]
			c := palette[paletteIndex]
			if !paletteValid[paletteIndex] {
				c = color.NRGBAModel.Convert(img.Palette[paletteIndex]).(color.NRGBA)
				palette[paletteIndex] = c
				paletteValid[paletteIndex] = true
			}
			if _, ok := index[c]; ok {
				continue
			}
			if len(table) == 256 {
				return nil, nil, false
			}
			index[c] = uint8(len(table))
			table = append(table, c)
		}
	}
	return table, index, true
}

func vp8lColorTableIndex(table []color.NRGBA) map[color.NRGBA]uint8 {
	index := make(map[color.NRGBA]uint8, len(table))
	for i, c := range table {
		index[c] = uint8(i)
	}
	return index
}

func vp8lSortedColorTable(table []color.NRGBA) []color.NRGBA {
	sortedTable := append([]color.NRGBA(nil), table...)
	sort.Slice(sortedTable, func(i int, j int) bool {
		return vp8lColorTableSortKey(sortedTable[i]) < vp8lColorTableSortKey(sortedTable[j])
	})
	return sortedTable
}

func vp8lColorTableSortKey(c color.NRGBA) uint32 {
	return uint32(c.A)<<24 | uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
}

func vp8lColorTablesEqual(a []color.NRGBA, b []color.NRGBA) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func vp8lColorIndexWidthBits(colorTableSize int) uint8 {
	switch {
	case colorTableSize <= 2:
		return 3
	case colorTableSize <= 4:
		return 2
	case colorTableSize <= 16:
		return 1
	default:
		return 0
	}
}

func makeVP8LColorCachePlan(readPixel pixelReader, bounds image.Rectangle, width int, height int, base vp8lEncodingPlan, maxBits uint64) (vp8lEncodingPlan, bool) {
	return makeVP8LColorCachePlanConfig(readPixel, bounds, width, height, base, maxBits, vp8lDefaultEncodingConfig())
}

func makeVP8LColorCachePlanConfig(readPixel pixelReader, bounds image.Rectangle, width int, height int, base vp8lEncodingPlan, maxBits uint64, cfg vp8lEncodingConfig) (vp8lEncodingPlan, bool) {
	mainWidth, mainHeight := vp8lPlanImageDimensions(width, height, base)
	mainBounds := image.Rect(0, 0, mainWidth, mainHeight)
	if !base.colorIndexing {
		mainBounds = bounds
	}
	read := vp8lPlanPixelReader(readPixel, bounds, width, height, base)
	if !vp8lShouldTryColorCache(read, mainBounds, mainWidth) {
		return vp8lEncodingPlan{}, false
	}
	best := vp8lEncodingPlan{}
	bestBits := maxBits
	ok := false
	for bits := uint8(vp8lMinColorCacheBits); bits <= vp8lMaxColorCacheBits; bits++ {
		_, literalAnalysis, greenCounts, cacheHits, tokenCount := vp8lBuildColorCache(read, mainBounds, mainWidth, bits, false, 0)
		if cacheHits == 0 {
			continue
		}
		greenLengthLimit := nLiteralCodes + nLengthCodes + 1<<bits
		greenLengths, lengthsOK := huffmanColorCacheCodeLengths(greenCounts[:greenLengthLimit])
		if !lengthsOK {
			continue
		}
		candidate := base
		candidate.colorCache = &vp8lColorCachePlan{
			bits:     bits,
			analysis: literalAnalysis,
			counts:   greenCounts,
			lengths:  greenLengths,
			codes:    canonicalColorCacheCodes(greenLengths),
		}
		candidateBits := vp8lPayloadBits(width, height, candidate)
		if candidateBits >= bestBits {
			if tokenCount > vp8lMaxMetaPrefixColorCacheTokens {
				continue
			}
		}
		var tokens []vp8lToken
		if candidateBits < bestBits || tokenCount <= vp8lMaxMetaPrefixColorCacheTokens {
			tokens, _, _, _, _ = vp8lBuildColorCache(read, mainBounds, mainWidth, bits, true, tokenCount)
		}
		if candidateBits < bestBits {
			candidate.colorCache.tokens = tokens
			best = candidate
			bestBits = candidateBits
			ok = true
		}
		if cfg.tryMetaPrefix && tokenCount <= vp8lMaxMetaPrefixColorCacheTokens {
			metaBase := candidate
			metaBase.colorCache.tokens = tokens
			if metaPrefixPlan, metaOK := makeVP8LMetaPrefixColorCachePlan(read, mainBounds, mainWidth, mainHeight, metaBase, tokens, bestBits); metaOK {
				best = metaPrefixPlan
				bestBits = vp8lPayloadBits(width, height, best)
				ok = true
			}
		}
	}
	return best, ok
}

func vp8lShouldTryColorCache(readPixel pixelReader, bounds image.Rectangle, width int) bool {
	total := bounds.Dx() * bounds.Dy()
	if total < 16 {
		return false
	}
	step := 1
	if total > vp8lColorCacheSampleSize {
		step = total / vp8lColorCacheSampleSize
	}
	var cache [vp8lMaxColorCacheSize]color.NRGBA
	hits := 0
	samples := 0
	for pos := 0; pos < total && samples < vp8lColorCacheSampleSize; pos += step {
		pixel := vp8lPixelAt(readPixel, bounds, width, pos)
		index := vp8lColorCacheIndex(pixel, vp8lMaxColorCacheBits)
		if cache[index] == pixel {
			hits++
		} else {
			cache[index] = pixel
		}
		samples++
	}
	return hits >= 16 && hits*100 >= samples*10
}

func vp8lBuildColorCache(readPixel pixelReader, bounds image.Rectangle, width int, bits uint8, collectTokens bool, tokenCapacity int) ([]vp8lToken, imageAnalysis, [nColorCacheGreenCodes]uint32, int, int) {
	var tokens []vp8lToken
	if collectTokens && tokenCapacity > 0 {
		tokens = make([]vp8lToken, 0, tokenCapacity)
	}
	var literalAnalysis imageAnalysis
	var greenCounts [nColorCacheGreenCodes]uint32
	var cache [vp8lMaxColorCacheSize]color.NRGBA
	firstLiteral := true
	cacheHits := 0
	tokenCount := 0
	total := bounds.Dx() * bounds.Dy()
	for pos := 0; pos < total; pos++ {
		pixel := vp8lPixelAt(readPixel, bounds, width, pos)
		index := vp8lColorCacheIndex(pixel, bits)
		tokenCount++
		if cache[index] == pixel {
			if collectTokens {
				tokens = append(tokens, vp8lToken{cacheIndex: index, colorCache: true})
			}
			greenCounts[nLiteralCodes+nLengthCodes+index]++
			cacheHits++
		} else {
			if collectTokens {
				tokens = append(tokens, vp8lToken{pixel: pixel})
			}
			observeVP8LLiteral(&literalAnalysis, &firstLiteral, pixel)
			greenCounts[pixel.G]++
			cache[index] = pixel
		}
	}
	if firstLiteral {
		literalAnalysis = emptyVP8LLiteralAnalysis()
	} else {
		literalAnalysis.finalizeChannels()
	}
	return tokens, literalAnalysis, greenCounts, cacheHits, tokenCount
}

func emptyVP8LLiteralAnalysis() imageAnalysis {
	return imageAnalysis{
		channels: [4]channelPlan{
			newConstantChannelPlan(0),
			newConstantChannelPlan(0),
			newConstantChannelPlan(0),
			newConstantChannelPlan(0),
		},
	}
}

func vp8lColorCacheIndex(pixel color.NRGBA, bits uint8) int {
	colorValue := uint32(pixel.A)<<24 | uint32(pixel.R)<<16 | uint32(pixel.G)<<8 | uint32(pixel.B)
	return int((0x1e35a7bd * colorValue) >> (32 - bits))
}

func huffmanColorCacheCodeLengths(counts []uint32) ([nColorCacheGreenCodes]uint8, bool) {
	var lengths [nColorCacheGreenCodes]uint8
	return lengths, huffmanCodeLengthsInto(lengths[:len(counts)], counts)
}

func canonicalColorCacheCodes(lengths [nColorCacheGreenCodes]uint8) [nColorCacheGreenCodes]uint16 {
	return canonicalChannelCodes(lengths[:])
}

func canonicalChannelCodes(lengths []uint8) [nColorCacheGreenCodes]uint16 {
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

	var codes [nColorCacheGreenCodes]uint16
	for symbol, length := range lengths {
		if length == 0 {
			continue
		}
		codes[symbol] = nextCodes[length]
		nextCodes[length]++
	}
	return codes
}

func vp8lPlanImageDimensions(width int, height int, plan vp8lEncodingPlan) (int, int) {
	if plan.colorIndexing {
		return divRoundUp(width, 1<<plan.colorIndexWidthBits), height
	}
	return width, height
}

func vp8lMetaPrefixImageDimensions(width int, height int, prefixBits uint8) (int, int) {
	blockSize := 1 << prefixBits
	return divRoundUp(width, blockSize), divRoundUp(height, blockSize)
}

func vp8lMetaPrefixIndex(x int, y int, prefixBits uint8, prefixImageWidth int) int {
	return (y>>prefixBits)*prefixImageWidth + (x >> prefixBits)
}

func makeVP8LMetaPrefixPlan(readPixel pixelReader, bounds image.Rectangle, width int, height int, base vp8lEncodingPlan, maxBits uint64) (vp8lEncodingPlan, bool) {
	if base.colorCache != nil || base.lz77 || base.colorIndexing {
		return vp8lEncodingPlan{}, false
	}
	read := vp8lPlanPixelReader(readPixel, bounds, width, height, base)
	bestBits := maxBits
	best := vp8lEncodingPlan{}
	found := false
	for prefixBits := uint8(vp8lMinMetaPrefixCandidateBits); prefixBits <= vp8lMaxMetaPrefixBits; prefixBits++ {
		candidate, ok := makeVP8LMetaPrefixPlanForBits(read, bounds, width, height, base, prefixBits)
		if !ok {
			continue
		}
		candidateBits := vp8lPayloadBits(width, height, candidate)
		if candidateBits >= bestBits {
			continue
		}
		best = candidate
		bestBits = candidateBits
		found = true
	}
	return best, found
}

func makeVP8LMetaPrefixLZ77Plan(readPixel pixelReader, bounds image.Rectangle, width int, height int, base vp8lEncodingPlan, tokens []vp8lToken, maxBits uint64, maxTokens int) (vp8lEncodingPlan, bool) {
	if !base.lz77 || base.colorCache != nil || base.colorIndexing || len(tokens) == 0 || len(tokens) > maxTokens {
		return vp8lEncodingPlan{}, false
	}
	bestBits := maxBits
	best := vp8lEncodingPlan{}
	found := false
	for prefixBits := uint8(vp8lMinMetaPrefixCandidateBits); prefixBits <= vp8lMaxMetaPrefixBits; prefixBits++ {
		candidate, ok := makeVP8LMetaPrefixLZ77PlanForBits(readPixel, bounds, width, height, base, tokens, prefixBits)
		if !ok {
			continue
		}
		candidateBits := vp8lPayloadBits(width, height, candidate)
		if candidateBits >= bestBits {
			continue
		}
		best = candidate
		bestBits = candidateBits
		found = true
	}
	return best, found
}

func makeVP8LTokenMetaPrefixLZ77Plan(readPixel pixelReader, bounds image.Rectangle, width int, height int, base vp8lEncodingPlan, tokens []vp8lToken, maxBits uint64, maxTokens int) (vp8lEncodingPlan, bool) {
	if !base.lz77 || base.colorCache != nil || base.colorIndexing || len(tokens) == 0 || len(tokens) > maxTokens {
		return vp8lEncodingPlan{}, false
	}
	prefixBits, ok := vp8lTokenMetaPrefixCandidateBits(width, height)
	if !ok {
		return vp8lEncodingPlan{}, false
	}
	candidate, ok := makeVP8LTokenMetaPrefixLZ77PlanForBits(width, height, base, tokens, prefixBits)
	if !ok || vp8lPayloadBits(width, height, candidate) >= maxBits {
		return vp8lEncodingPlan{}, false
	}
	return candidate, true
}

func vp8lTokenMetaPrefixCandidateBits(width int, height int) (uint8, bool) {
	for prefixBits := uint8(vp8lMaxMetaPrefixBits); ; prefixBits-- {
		prefixWidth, prefixHeight := vp8lMetaPrefixImageDimensions(width, height, prefixBits)
		prefixBlocks := prefixWidth * prefixHeight
		if prefixBlocks >= 2 && prefixBlocks <= vp8lMaxMetaPrefixBlocks {
			return prefixBits, true
		}
		if prefixBits == vp8lMinMetaPrefixCandidateBits {
			return 0, false
		}
	}
}

func makeVP8LMetaPrefixColorCachePlan(readPixel pixelReader, bounds image.Rectangle, width int, height int, base vp8lEncodingPlan, tokens []vp8lToken, maxBits uint64) (vp8lEncodingPlan, bool) {
	if base.colorCache == nil || base.lz77 || base.colorIndexing || len(tokens) == 0 || len(tokens) > vp8lMaxMetaPrefixColorCacheTokens {
		return vp8lEncodingPlan{}, false
	}
	bestBits := maxBits
	best := vp8lEncodingPlan{}
	found := false
	for prefixBits := uint8(vp8lMinMetaPrefixCandidateBits); prefixBits <= vp8lMaxMetaPrefixBits; prefixBits++ {
		candidate, ok := makeVP8LMetaPrefixColorCachePlanForBits(readPixel, bounds, width, height, base, tokens, prefixBits)
		if !ok {
			continue
		}
		candidateBits := vp8lPayloadBits(width, height, candidate)
		if candidateBits >= bestBits {
			continue
		}
		best = candidate
		bestBits = candidateBits
		found = true
	}
	return best, found
}

func makeVP8LMetaPrefixPlanForBits(readPixel pixelReader, bounds image.Rectangle, width int, height int, base vp8lEncodingPlan, prefixBits uint8) (vp8lEncodingPlan, bool) {
	prefixWidth, prefixHeight := vp8lMetaPrefixImageDimensions(width, height, prefixBits)
	prefixBlocks := prefixWidth * prefixHeight
	if prefixBlocks < 2 || prefixBlocks > vp8lMaxMetaPrefixBlocks {
		return vp8lEncodingPlan{}, false
	}

	groupImage := make([]uint16, prefixBlocks)
	groups := make([]imageAnalysis, 0, minInt(prefixBlocks, vp8lMaxMetaPrefixGroups))
	groupPixels := make([]int, 0, minInt(prefixBlocks, vp8lMaxMetaPrefixGroups))
	for by := 0; by < prefixHeight; by++ {
		for bx := 0; bx < prefixWidth; bx++ {
			analysis := analyzeVP8LMetaPrefixBlock(readPixel, bounds, width, height, prefixBits, bx, by)
			blockPixels := vp8lMetaPrefixBlockPixelCount(width, height, prefixBits, bx, by)
			groupIndex := -1
			for i, group := range groups {
				if group.codingEqual(analysis) {
					groupIndex = i
					break
				}
			}
			if groupIndex >= 0 {
				groupPixels[groupIndex] += blockPixels
			} else if mergeIndex, merged, mergeDelta, ok := vp8lBestMetaPrefixGroupMerge(groups, groupPixels, analysis, blockPixels); ok && (mergeDelta <= 0 || len(groups) >= vp8lMaxMetaPrefixGroups) {
				groupIndex = mergeIndex
				groups[groupIndex] = merged
				groupPixels[groupIndex] += blockPixels
			} else {
				if len(groups) >= vp8lMaxMetaPrefixGroups {
					return vp8lEncodingPlan{}, false
				}
				groupIndex = len(groups)
				groups = append(groups, analysis)
				groupPixels = append(groupPixels, blockPixels)
			}
			groupImage[by*prefixWidth+bx] = uint16(groupIndex)
		}
	}
	if len(groups) < 2 {
		return vp8lEncodingPlan{}, false
	}
	if vp8lMetaPrefixReferencedGroupCount(groupImage) != len(groups) {
		return vp8lEncodingPlan{}, false
	}

	imageBounds := image.Rect(0, 0, prefixWidth, prefixHeight)
	imageAnalysis := analyzeImage(vp8lMetaPrefixImageReader(groupImage, prefixWidth), imageBounds)
	candidate := base
	candidate.metaPrefix = &vp8lMetaPrefixPlan{
		prefixBits:    prefixBits,
		width:         prefixWidth,
		height:        prefixHeight,
		image:         groupImage,
		imageAnalysis: imageAnalysis,
		groups:        groups,
		groupPixels:   groupPixels,
	}
	return candidate, true
}

func makeVP8LMetaPrefixColorCachePlanForBits(readPixel pixelReader, bounds image.Rectangle, width int, height int, base vp8lEncodingPlan, tokens []vp8lToken, prefixBits uint8) (vp8lEncodingPlan, bool) {
	candidate, ok := makeVP8LMetaPrefixPlanForBits(readPixel, bounds, width, height, base, prefixBits)
	if !ok {
		return vp8lEncodingPlan{}, false
	}
	colorCacheGroups, groupTokens, ok := vp8lBuildMetaPrefixColorCacheGroups(candidate.metaPrefix, tokens, width, width*height, base.analysis, base.colorCache.bits)
	if !ok {
		return vp8lEncodingPlan{}, false
	}
	candidate.colorCache.tokens = tokens
	candidate.metaPrefix.colorCacheGroups = colorCacheGroups
	candidate.metaPrefix.groupTokens = groupTokens
	return candidate, true
}

func vp8lBuildMetaPrefixColorCacheGroups(metaPrefix *vp8lMetaPrefixPlan, tokens []vp8lToken, width int, total int, baseAnalysis imageAnalysis, bits uint8) ([]vp8lColorCacheGroupPlan, []int, bool) {
	colorCacheGroups := make([]vp8lColorCacheGroupPlan, len(metaPrefix.groups))
	groupTokens := make([]int, len(metaPrefix.groups))
	observers := make([]vp8lLiteralAnalysisObserver, len(metaPrefix.groups))
	for i := range observers {
		observers[i] = newVP8LLiteralAnalysisObserver(baseAnalysis)
	}
	greenLimit := nLiteralCodes + nLengthCodes + 1<<bits
	pos := 0
	for _, token := range tokens {
		if pos >= total || token.copyLength > 0 {
			return nil, nil, false
		}
		groupIndex := vp8lMetaPrefixGroupAt(metaPrefix, pos%width, pos/width)
		if groupIndex < 0 || groupIndex >= len(colorCacheGroups) {
			return nil, nil, false
		}
		group := &colorCacheGroups[groupIndex]
		groupTokens[groupIndex]++
		if token.colorCache {
			group.counts[nLiteralCodes+nLengthCodes+token.cacheIndex]++
			pos++
			continue
		}
		observers[groupIndex].observePixel(token.pixel)
		group.counts[token.pixel.G]++
		pos++
	}
	if pos != total {
		return nil, nil, false
	}
	for i := range colorCacheGroups {
		if groupTokens[i] == 0 {
			colorCacheGroups[i].counts[0] = 1
		}
		greenLengths, ok := huffmanColorCacheCodeLengths(colorCacheGroups[i].counts[:greenLimit])
		if !ok {
			return nil, nil, false
		}
		colorCacheGroups[i].literalAnalysis = observers[i].result()
		colorCacheGroups[i].lengths = greenLengths
		colorCacheGroups[i].codes = canonicalColorCacheCodes(greenLengths)
	}
	return colorCacheGroups, groupTokens, true
}

func vp8lMetaPrefixReferencedGroupCount(groupImage []uint16) int {
	maxGroup := -1
	for _, group := range groupImage {
		if int(group) > maxGroup {
			maxGroup = int(group)
		}
	}
	return maxGroup + 1
}

func makeVP8LMetaPrefixLZ77PlanForBits(readPixel pixelReader, bounds image.Rectangle, width int, height int, base vp8lEncodingPlan, tokens []vp8lToken, prefixBits uint8) (vp8lEncodingPlan, bool) {
	candidate, ok := makeVP8LMetaPrefixPlanForBits(readPixel, bounds, width, height, base, prefixBits)
	if !ok {
		return vp8lEncodingPlan{}, false
	}
	lz77Groups, groupTokens, ok := vp8lBuildMetaPrefixLZ77Groups(candidate.metaPrefix, tokens, width, width*height, base.analysis)
	if !ok {
		return vp8lEncodingPlan{}, false
	}
	candidate.lz77Tokens = tokens
	candidate.metaPrefix.lz77Groups = lz77Groups
	candidate.metaPrefix.groupTokens = groupTokens
	return candidate, true
}

func makeVP8LTokenMetaPrefixLZ77PlanForBits(width int, height int, base vp8lEncodingPlan, tokens []vp8lToken, prefixBits uint8) (vp8lEncodingPlan, bool) {
	metaPrefix, ok := makeVP8LTokenMetaPrefixPlan(tokens, width, height, prefixBits, base.analysis)
	if !ok {
		return vp8lEncodingPlan{}, false
	}
	candidate := base
	candidate.lz77Tokens = tokens
	candidate.metaPrefix = metaPrefix
	return candidate, true
}

func vp8lBuildMetaPrefixLZ77Groups(metaPrefix *vp8lMetaPrefixPlan, tokens []vp8lToken, width int, total int, baseAnalysis imageAnalysis) ([]vp8lLZ77GroupPlan, []int, bool) {
	lz77Groups := make([]vp8lLZ77GroupPlan, len(metaPrefix.groups))
	groupTokens := make([]int, len(metaPrefix.groups))
	observers := make([]vp8lLiteralAnalysisObserver, len(metaPrefix.groups))
	for i := range observers {
		observers[i] = newVP8LLiteralAnalysisObserver(baseAnalysis)
	}
	pos := 0
	for _, token := range tokens {
		if pos >= total {
			return nil, nil, false
		}
		groupIndex := vp8lMetaPrefixGroupAt(metaPrefix, pos%width, pos/width)
		if groupIndex < 0 || groupIndex >= len(lz77Groups) {
			return nil, nil, false
		}
		group := &lz77Groups[groupIndex]
		groupTokens[groupIndex]++
		if token.copyLength > 0 {
			if pos+token.copyLength > total {
				return nil, nil, false
			}
			lengthPrefix := vp8lPrefixCode(token.copyLength)
			distancePrefix := vp8lDistancePrefixCode(token.distanceCode)
			group.greenCounts[nLiteralCodes+lengthPrefix.code]++
			group.distanceCounts[distancePrefix.code]++
			pos += token.copyLength
			continue
		}
		observers[groupIndex].observePixel(token.pixel)
		group.greenCounts[token.pixel.G]++
		pos++
	}
	if pos != total {
		return nil, nil, false
	}
	for i := range lz77Groups {
		if groupTokens[i] == 0 {
			lz77Groups[i].greenCounts[0] = 1
		}
		greenLengths, ok := huffmanCodeLengths(lz77Groups[i].greenCounts)
		if !ok {
			return nil, nil, false
		}
		distanceN, distanceSymbols, distanceLengths, distanceCodes, distanceNormal, ok := vp8lDistanceCodeFor(lz77Groups[i].distanceCounts)
		if !ok && !vp8lDistanceCountsEmpty(lz77Groups[i].distanceCounts) {
			return nil, nil, false
		}
		lz77Groups[i].literalAnalysis = observers[i].result()
		lz77Groups[i].greenLengths = greenLengths
		lz77Groups[i].greenCodes = canonicalCodes(greenLengths)
		lz77Groups[i].distanceN = distanceN
		lz77Groups[i].distanceSymbols = distanceSymbols
		lz77Groups[i].distanceLengths = distanceLengths
		lz77Groups[i].distanceCodes = distanceCodes
		lz77Groups[i].distanceNormal = distanceNormal
	}
	return lz77Groups, groupTokens, true
}

func vp8lDistanceCountsEmpty(counts [nDistanceCodes]uint32) bool {
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func analyzeVP8LMetaPrefixBlock(readPixel pixelReader, bounds image.Rectangle, width int, height int, prefixBits uint8, bx int, by int) imageAnalysis {
	return analyzeImage(readPixel, vp8lMetaPrefixBlockBounds(bounds, width, height, prefixBits, bx, by))
}

func vp8lMetaPrefixBlockBounds(bounds image.Rectangle, width int, height int, prefixBits uint8, bx int, by int) image.Rectangle {
	x0 := bounds.Min.X + bx*(1<<prefixBits)
	y0 := bounds.Min.Y + by*(1<<prefixBits)
	x1 := minInt(x0+(1<<prefixBits), bounds.Min.X+width)
	y1 := minInt(y0+(1<<prefixBits), bounds.Min.Y+height)
	return image.Rect(x0, y0, x1, y1)
}

func vp8lMetaPrefixBlockPixelCount(width int, height int, prefixBits uint8, bx int, by int) int {
	x0 := bx * (1 << prefixBits)
	y0 := by * (1 << prefixBits)
	x1 := minInt(x0+(1<<prefixBits), width)
	y1 := minInt(y0+(1<<prefixBits), height)
	return (x1 - x0) * (y1 - y0)
}

func vp8lBestMetaPrefixGroupMerge(groups []imageAnalysis, groupPixels []int, block imageAnalysis, blockPixels int) (int, imageAnalysis, int64, bool) {
	bestIndex := -1
	bestDelta := int64(0)
	var bestMerged imageAnalysis
	for i, group := range groups {
		merged := group.merge(block)
		delta := vp8lMetaPrefixGroupMergeDelta(group, groupPixels[i], block, blockPixels, merged)
		if bestIndex < 0 || delta < bestDelta {
			bestIndex = i
			bestDelta = delta
			bestMerged = merged
		}
	}
	return bestIndex, bestMerged, bestDelta, bestIndex >= 0
}

func vp8lMetaPrefixGroupMergeDelta(group imageAnalysis, groupPixels int, block imageAnalysis, blockPixels int, merged imageAnalysis) int64 {
	before := vp8lMetaPrefixGroupBits(group, groupPixels) + vp8lMetaPrefixGroupBits(block, blockPixels)
	after := vp8lMetaPrefixGroupBits(merged, groupPixels+blockPixels)
	if after >= before {
		return int64(after - before)
	}
	return -int64(before - after)
}

func vp8lMetaPrefixGroupBits(analysis imageAnalysis, pixels int) uint64 {
	return imageAnalysisTreeAndDataBits(analysis, pixels)
}

func vp8lMetaPrefixImageReader(groupImage []uint16, width int) pixelReader {
	return func(x int, y int) color.NRGBA {
		code := groupImage[y*width+x]
		return color.NRGBA{
			R: uint8(code >> 8),
			G: uint8(code),
			A: 255,
		}
	}
}

func vp8lMetaPrefixCode(c color.NRGBA) int {
	return int(c.R)<<8 | int(c.G)
}

func vp8lMetaPrefixGroupAt(metaPrefix *vp8lMetaPrefixPlan, x int, y int) int {
	return int(metaPrefix.image[vp8lMetaPrefixIndex(x, y, metaPrefix.prefixBits, metaPrefix.width)])
}

func vp8lColorTableImageReader(table []color.NRGBA) pixelReader {
	return func(x int, y int) color.NRGBA {
		if x < 0 || x >= len(table) || y != 0 {
			return color.NRGBA{}
		}
		if x == 0 {
			return table[0]
		}
		return subtractNRGBA(table[x], table[x-1])
	}
}

func vp8lColorIndexedImageReader(readPixel pixelReader, bounds image.Rectangle, width int, widthBits uint8, index map[color.NRGBA]uint8) pixelReader {
	const cacheSize = 256
	var cacheKeys [cacheSize]color.NRGBA
	var cacheValues [cacheSize]uint8
	var cacheValid [cacheSize]bool
	lookup := func(c color.NRGBA) uint8 {
		cacheIndex := vp8lColorIndexCacheIndex(c)
		if cacheValid[cacheIndex] && cacheKeys[cacheIndex] == c {
			return cacheValues[cacheIndex]
		}
		v := index[c]
		cacheKeys[cacheIndex] = c
		cacheValues[cacheIndex] = v
		cacheValid[cacheIndex] = true
		return v
	}
	return func(x int, y int) color.NRGBA {
		localY := y
		if widthBits == 0 {
			c := readPixel(bounds.Min.X+x, bounds.Min.Y+localY)
			return color.NRGBA{G: lookup(c), A: 255}
		}

		groupSize := 1 << widthBits
		bitsPerIndex := 8 / groupSize
		var packed uint8
		for i := 0; i < groupSize; i++ {
			localX := x*groupSize + i
			if localX >= width {
				continue
			}
			c := readPixel(bounds.Min.X+localX, bounds.Min.Y+localY)
			packed |= lookup(c) << uint(i*bitsPerIndex)
		}
		return color.NRGBA{G: packed, A: 255}
	}
}

func vp8lPalettedColorIndexedImageReader(m image.Image, bounds image.Rectangle, width int, widthBits uint8, index map[color.NRGBA]uint8) (pixelReader, bool) {
	img, ok := m.(*image.Paletted)
	if !ok || len(img.Palette) == 0 {
		return nil, false
	}
	var paletteIndex [256]uint8
	for i, c := range img.Palette {
		if i == len(paletteIndex) {
			break
		}
		paletteIndex[i] = index[color.NRGBAModel.Convert(c).(color.NRGBA)]
	}
	pix := img.Pix
	stride := img.Stride
	minX := img.Rect.Min.X
	minY := img.Rect.Min.Y
	boundsMinX := bounds.Min.X
	boundsMinY := bounds.Min.Y
	rowStart := func(y int) int {
		return (boundsMinY+y-minY)*stride + boundsMinX - minX
	}
	switch widthBits {
	case 0:
		return func(x int, y int) color.NRGBA {
			return color.NRGBA{G: paletteIndex[pix[rowStart(y)+x]], A: 255}
		}, true
	case 1:
		return func(x int, y int) color.NRGBA {
			row := rowStart(y)
			localX := x << 1
			packed := paletteIndex[pix[row+localX]]
			if localX+1 < width {
				packed |= paletteIndex[pix[row+localX+1]] << 4
			}
			return color.NRGBA{G: packed, A: 255}
		}, true
	case 2:
		return func(x int, y int) color.NRGBA {
			row := rowStart(y)
			localX := x << 2
			packed := paletteIndex[pix[row+localX]]
			if localX+1 < width {
				packed |= paletteIndex[pix[row+localX+1]] << 2
			}
			if localX+2 < width {
				packed |= paletteIndex[pix[row+localX+2]] << 4
			}
			if localX+3 < width {
				packed |= paletteIndex[pix[row+localX+3]] << 6
			}
			return color.NRGBA{G: packed, A: 255}
		}, true
	default:
		return func(x int, y int) color.NRGBA {
			row := rowStart(y)
			localX := x << 3
			packed := paletteIndex[pix[row+localX]]
			if localX+1 < width {
				packed |= paletteIndex[pix[row+localX+1]] << 1
			}
			if localX+2 < width {
				packed |= paletteIndex[pix[row+localX+2]] << 2
			}
			if localX+3 < width {
				packed |= paletteIndex[pix[row+localX+3]] << 3
			}
			if localX+4 < width {
				packed |= paletteIndex[pix[row+localX+4]] << 4
			}
			if localX+5 < width {
				packed |= paletteIndex[pix[row+localX+5]] << 5
			}
			if localX+6 < width {
				packed |= paletteIndex[pix[row+localX+6]] << 6
			}
			if localX+7 < width {
				packed |= paletteIndex[pix[row+localX+7]] << 7
			}
			return color.NRGBA{G: packed, A: 255}
		}, true
	}
}

func vp8lColorIndexCacheIndex(c color.NRGBA) uint8 {
	h := uint32(c.R)*0x1e35a7bd ^ uint32(c.G)*0x94d049bb ^ uint32(c.B)*0x45d9f3b ^ uint32(c.A)*0x119de1f3
	return uint8(h >> 24)
}

func makeVP8LLZ77Plan(readPixel pixelReader, bounds image.Rectangle, width int, height int, base vp8lEncodingPlan, maxBits uint64) (vp8lEncodingPlan, bool) {
	return makeVP8LLZ77PlanConfig(readPixel, bounds, width, height, base, maxBits, vp8lDefaultEncodingConfig())
}

func makeVP8LLZ77PlanConfig(readPixel pixelReader, bounds image.Rectangle, width int, height int, base vp8lEncodingPlan, maxBits uint64, cfg vp8lEncodingConfig) (vp8lEncodingPlan, bool) {
	best := vp8lEncodingPlan{}
	bestBits := maxBits
	found := false
	mainWidth, mainHeight := vp8lPlanImageDimensions(width, height, base)
	total := mainWidth * mainHeight
	for _, candidateCount := range vp8lLZ77CandidateCounts(total) {
		candidate, candidateBits, ok := makeVP8LLZ77PlanConfigCandidateCount(readPixel, bounds, width, height, base, bestBits, cfg, candidateCount)
		if !ok || candidateBits >= bestBits {
			continue
		}
		best = candidate
		bestBits = candidateBits
		found = true
	}
	return best, found
}

func makeVP8LLZ77PlanConfigCandidateCount(readPixel pixelReader, bounds image.Rectangle, width int, height int, base vp8lEncodingPlan, maxBits uint64, cfg vp8lEncodingConfig, candidateCount int) (vp8lEncodingPlan, uint64, bool) {
	if base.analysis.allChannelsConstant() {
		return vp8lEncodingPlan{}, 0, false
	}
	mainWidth, mainHeight := vp8lPlanImageDimensions(width, height, base)
	total := mainWidth * mainHeight
	mainBounds := image.Rect(0, 0, mainWidth, mainHeight)
	if !base.colorIndexing {
		mainBounds = bounds
	}
	read := vp8lPlanPixelReader(readPixel, bounds, width, height, base)
	if !vp8lShouldTryLZ77(read, mainBounds, mainWidth) {
		return vp8lEncodingPlan{}, 0, false
	}
	lz77Tokens, literalAnalysis, greenCounts, distanceCounts, copyCount, tokenCount := vp8lBuildLZ77WithAnalysisCandidateCount(read, mainBounds, mainWidth, true, 0, base.analysis, candidateCount)
	if copyCount == 0 {
		return vp8lEncodingPlan{}, 0, false
	}
	literalBase := base
	base, ok := vp8lPlanWithLZ77(base, lz77Tokens, vp8lLZ77Statistics{
		literalAnalysis: literalAnalysis,
		greenCounts:     greenCounts,
		distanceCounts:  distanceCounts,
		copyCount:       copyCount,
	})
	if !ok {
		return vp8lEncodingPlan{}, 0, false
	}
	if cfg.optimalLZ77Passes > 0 && base.colorIndexing && total <= cfg.maxOptimalLZ77Pixels && candidateCount == vp8lOptimalLZ77CandidateCount(total) {
		if optimized, improved := vp8lOptimizeLZ77Plan(read, mainBounds, mainWidth, width, height, literalBase, base, candidateCount, cfg.optimalLZ77Passes); improved {
			base = optimized
			lz77Tokens = optimized.lz77Tokens
			tokenCount = len(lz77Tokens)
		}
	}
	best := vp8lEncodingPlan{}
	bestBits := maxBits
	found := false
	lz77Bits := vp8lPayloadBits(width, height, base)
	if lz77Bits < bestBits {
		best = base
		bestBits = lz77Bits
		found = true
	}
	if cfg.tryColorCache && vp8lShouldTryLZ77ColorCacheConfig(base, width, height, lz77Bits, maxBits, tokenCount, cfg) {
		colorCacheMaxBits := bestBits
		if vp8lPlanUsesTransform(base) {
			minSavings := vp8lTransformedLZ77ColorCacheMinSavings(bestBits)
			if colorCacheMaxBits <= minSavings {
				return best, bestBits, found
			}
			colorCacheMaxBits -= minSavings
		}
		if colorCachePlan, ok := makeVP8LLZ77ColorCachePlan(read, mainBounds, mainWidth, width, height, base, lz77Tokens, colorCacheMaxBits); ok {
			best = colorCachePlan
			bestBits = vp8lPayloadBits(width, height, best)
			found = true
		}
	}
	if cfg.tryLZ77MetaPrefix && lz77Bits < maxBits && tokenCount <= cfg.maxMetaPrefixLZ77Tokens {
		if metaPrefixPlan, ok := makeVP8LMetaPrefixLZ77Plan(read, mainBounds, mainWidth, mainHeight, base, lz77Tokens, bestBits, cfg.maxMetaPrefixLZ77Tokens); ok {
			best = metaPrefixPlan
			bestBits = vp8lPayloadBits(width, height, best)
			found = true
		}
	}
	if cfg.tryLZ77TokenMetaPrefix && lz77Bits < maxBits && tokenCount <= cfg.maxMetaPrefixLZ77Tokens {
		if metaPrefixPlan, ok := makeVP8LTokenMetaPrefixLZ77Plan(read, mainBounds, mainWidth, mainHeight, base, lz77Tokens, bestBits, cfg.maxMetaPrefixLZ77Tokens); ok {
			best = metaPrefixPlan
			bestBits = vp8lPayloadBits(width, height, best)
			found = true
		}
	}
	return best, bestBits, found
}

func vp8lLZ77CandidateCounts(total int) []int {
	defaultCount := vp8lHashCandidateCount(total)
	if defaultCount >= vp8lMaxHashCandidates || total > 512*512 {
		return []int{defaultCount}
	}
	return []int{defaultCount, vp8lMaxHashCandidates}
}

func vp8lOptimalLZ77CandidateCount(total int) int {
	counts := vp8lLZ77CandidateCounts(total)
	return counts[len(counts)-1]
}

func vp8lShouldTryLZ77ColorCache(base vp8lEncodingPlan, width int, height int, lz77Bits uint64, maxBits uint64, tokenCount int) bool {
	return vp8lShouldTryLZ77ColorCacheConfig(base, width, height, lz77Bits, maxBits, tokenCount, vp8lDefaultEncodingConfig())
}

func vp8lShouldTryLZ77ColorCacheConfig(base vp8lEncodingPlan, width int, height int, lz77Bits uint64, maxBits uint64, tokenCount int, cfg vp8lEncodingConfig) bool {
	if !cfg.tryLZ77ColorCache {
		return false
	}
	if !vp8lPlanUsesTransform(base) {
		return true
	}
	if !cfg.tryTransformedLZ77ColorCache {
		return false
	}
	if tokenCount > cfg.maxTransformedLZ77CacheTokens {
		return false
	}
	breakdown := vp8lPayloadBitBreakdownFor(width, height, base)
	return vp8lShouldTryTransformedLZ77ColorCache(base, breakdown, lz77Bits, maxBits)
}

func vp8lShouldTryTransformedLZ77ColorCache(base vp8lEncodingPlan, breakdown vp8lPayloadBitBreakdown, lz77Bits uint64, maxBits uint64) bool {
	if vp8lPlanTransformCount(base) != 1 {
		return false
	}
	if !base.predictor && !base.colorTransform {
		return false
	}
	if base.colorTransform && !vp8lColorTransformResidualLikelyHelpsLZ77ColorCache(base.analysis) {
		return false
	}
	if lz77Bits > maxBits {
		if !base.colorTransform {
			return false
		}
		slack := vp8lTransformedLZ77ColorCacheTrialSlack(maxBits)
		if lz77Bits-maxBits > slack {
			return false
		}
	}
	if breakdown.mainImageData < vp8lMinTransformedLZ77CacheBits {
		return false
	}
	transformBits := breakdown.transformHeaderBits() + breakdown.transformImageDataBits()
	return transformBits > 0 && transformBits*4 <= breakdown.mainImageData
}

func vp8lColorTransformResidualLikelyHelpsLZ77ColorCache(analysis imageAnalysis) bool {
	return vp8lCheapColorResidualChannel(analysis.channels[1]) || vp8lCheapColorResidualChannel(analysis.channels[2])
}

func vp8lCheapColorResidualChannel(ch channelPlan) bool {
	return ch.constant || ch.twoSymbol()
}

func vp8lTransformedLZ77ColorCacheTrialSlack(maxBits uint64) uint64 {
	return maxBits/8 + 4096
}

func vp8lTransformedLZ77ColorCacheMinSavings(lz77Bits uint64) uint64 {
	const minBits = 1024
	relativeBits := lz77Bits / 10
	if relativeBits > minBits {
		return relativeBits
	}
	return minBits
}

func vp8lShouldTryLZ77(readPixel pixelReader, bounds image.Rectangle, width int) bool {
	total := bounds.Dx() * bounds.Dy()
	if total < vp8lMinBackwardRefLength*2 {
		return false
	}
	const maxSamples = 2048
	step := 1
	if total > maxSamples {
		step = total / maxSamples
	}
	var seen [vp8lHashSize]uint32
	var occupied [vp8lHashSize]bool
	for pos := 0; pos+vp8lMinBackwardRefLength <= total; pos += step {
		hashValue := vp8lHashValueAt(readPixel, bounds, width, pos)
		hash := vp8lHashIndex(hashValue)
		if occupied[hash] && seen[hash] == hashValue {
			return true
		}
		seen[hash] = hashValue
		occupied[hash] = true
	}
	return false
}

func vp8lDistanceCodeFor(distanceCounts [nDistanceCodes]uint32) (int, [2]uint8, [nDistanceCodes]uint8, [nDistanceCodes]uint16, bool, bool) {
	var symbols [2]uint8
	n := 0
	for symbol, count := range distanceCounts {
		if count == 0 {
			continue
		}
		if n < len(symbols) {
			symbols[n] = uint8(symbol)
		}
		n++
	}
	switch n {
	case 0:
		return 0, symbols, [nDistanceCodes]uint8{}, [nDistanceCodes]uint16{}, false, false
	case 1, 2:
		return n, symbols, [nDistanceCodes]uint8{}, [nDistanceCodes]uint16{}, false, true
	default:
		lengths, ok := huffmanDistanceCodeLengths(distanceCounts)
		if !ok {
			return 0, symbols, [nDistanceCodes]uint8{}, [nDistanceCodes]uint16{}, false, false
		}
		return n, symbols, lengths, canonicalDistanceCodes(lengths), true, true
	}
}

type vp8lLiteralAnalysisObserver struct {
	analysis    imageAnalysis
	initialized [4]bool
	observe     [4]bool
}

func newVP8LLiteralAnalysisObserver(base imageAnalysis) vp8lLiteralAnalysisObserver {
	var o vp8lLiteralAnalysisObserver
	o.analysis.channels[0] = newConstantChannelPlan(0)
	o.initialized[0] = true
	for i := 1; i < len(o.analysis.channels); i++ {
		if base.channels[i].constant {
			o.analysis.channels[i] = newConstantChannelPlan(base.channels[i].value)
			o.initialized[i] = true
			continue
		}
		o.observe[i] = true
	}
	if base.channels[3].constant && base.channels[3].value != 255 {
		o.analysis.alpha = true
	}
	return o
}

func (o *vp8lLiteralAnalysisObserver) observePixel(pixel color.NRGBA) {
	values := [4]uint8{0, pixel.R, pixel.B, pixel.A}
	for i := 1; i < len(values); i++ {
		if !o.observe[i] {
			continue
		}
		v := values[i]
		if !o.initialized[i] {
			o.analysis.channels[i] = newConstantChannelPlan(v)
			o.initialized[i] = true
			continue
		}
		o.analysis.channels[i].observe(v)
	}
	o.analysis.alpha = o.analysis.alpha || pixel.A != 255
}

func (o *vp8lLiteralAnalysisObserver) result() imageAnalysis {
	for i := 1; i < len(o.analysis.channels); i++ {
		if !o.initialized[i] {
			o.analysis.channels[i] = newConstantChannelPlan(0)
			o.initialized[i] = true
		}
	}
	o.analysis.finalizeChannels()
	return o.analysis
}

func vp8lBuildLZ77(readPixel pixelReader, bounds image.Rectangle, width int, collectTokens bool, tokenCapacity int) ([]vp8lToken, imageAnalysis, [nLiteralCodes + nLengthCodes]uint32, [nDistanceCodes]uint32, int, int) {
	return vp8lBuildLZ77WithAnalysis(readPixel, bounds, width, collectTokens, tokenCapacity, imageAnalysis{})
}

func vp8lBuildLZ77WithAnalysis(readPixel pixelReader, bounds image.Rectangle, width int, collectTokens bool, tokenCapacity int, baseAnalysis imageAnalysis) ([]vp8lToken, imageAnalysis, [nLiteralCodes + nLengthCodes]uint32, [nDistanceCodes]uint32, int, int) {
	return vp8lBuildLZ77WithAnalysisCandidateCount(readPixel, bounds, width, collectTokens, tokenCapacity, baseAnalysis, vp8lHashCandidateCount(bounds.Dx()*bounds.Dy()))
}

func vp8lBuildLZ77WithAnalysisCandidateCount(readPixel pixelReader, bounds image.Rectangle, width int, collectTokens bool, tokenCapacity int, baseAnalysis imageAnalysis, candidateCount int) ([]vp8lToken, imageAnalysis, [nLiteralCodes + nLengthCodes]uint32, [nDistanceCodes]uint32, int, int) {
	var tokens []vp8lToken
	if collectTokens && tokenCapacity > 0 {
		tokens = make([]vp8lToken, 0, tokenCapacity)
	}
	literalObserver := newVP8LLiteralAnalysisObserver(baseAnalysis)
	var greenCounts [nLiteralCodes + nLengthCodes]uint32
	var distanceCounts [nDistanceCodes]uint32
	firstLiteral := true
	copyCount := 0
	tokenCount := 0
	total := bounds.Dx() * bounds.Dy()
	candidateCount = clipInt(candidateCount, vp8lMinHashCandidates, vp8lMaxHashCandidates)
	var primaryHashTable [vp8lHashSize][vp8lMinHashCandidates]int32
	var extraHashTable [vp8lHashSize][vp8lMinHashCandidates]int32
	vp8lInitHashTables(&primaryHashTable, &extraHashTable, candidateCount)

	emitLiteral := func(pixel color.NRGBA) {
		if collectTokens {
			tokens = append(tokens, vp8lToken{pixel: pixel})
		}
		tokenCount++
		firstLiteral = false
		literalObserver.observePixel(pixel)
		greenCounts[pixel.G]++
	}

	emitCopy := func(length int, distanceCode int) {
		lengthPrefix := vp8lPrefixCode(length)
		distancePrefix := vp8lDistancePrefixCode(distanceCode)
		if collectTokens {
			tokens = append(tokens, vp8lToken{copyLength: length, distanceCode: distanceCode})
		}
		tokenCount++
		greenCounts[nLiteralCodes+lengthPrefix.code]++
		distanceCounts[distancePrefix.code]++
		copyCount++
	}

	for pos := 0; pos < total; {
		if pos+vp8lMinBackwardRefLength <= total {
			hash := vp8lHashAt(readPixel, bounds, width, pos)
			candidates := vp8lHashCandidatesFor(primaryHashTable[hash], extraHashTable[hash], candidateCount)
			match := vp8lBestHashMatch(candidates, candidateCount, readPixel, bounds, width, pos, total)
			if match.length >= vp8lMinBackwardRefLength {
				nextMatch := vp8lNextLazyMatch(&primaryHashTable, &extraHashTable, candidateCount, hash, readPixel, bounds, width, pos, total)
				if vp8lShouldUseLazyMatch(match, nextMatch) {
					emitLiteral(vp8lPixelAt(readPixel, bounds, width, pos))
					vp8lInsertHash(&primaryHashTable, &extraHashTable, candidateCount, readPixel, bounds, width, pos, total)
					pos++
					continue
				}
				emitCopy(match.length, match.distanceCode)
				for i := 0; i < match.length; i++ {
					vp8lInsertHash(&primaryHashTable, &extraHashTable, candidateCount, readPixel, bounds, width, pos+i, total)
				}
				pos += match.length
				continue
			}
			vp8lInsertHash(&primaryHashTable, &extraHashTable, candidateCount, readPixel, bounds, width, pos, total)
		}
		emitLiteral(vp8lPixelAt(readPixel, bounds, width, pos))
		pos++
	}
	if firstLiteral {
		return tokens, emptyVP8LLiteralAnalysis(), greenCounts, distanceCounts, copyCount, tokenCount
	}
	return tokens, literalObserver.result(), greenCounts, distanceCounts, copyCount, tokenCount
}

func makeVP8LLZ77ColorCachePlan(readPixel pixelReader, bounds image.Rectangle, mainWidth int, width int, height int, base vp8lEncodingPlan, lz77Tokens []vp8lToken, maxBits uint64) (vp8lEncodingPlan, bool) {
	if !vp8lShouldTryColorCache(readPixel, bounds, mainWidth) {
		return vp8lEncodingPlan{}, false
	}
	best := vp8lEncodingPlan{}
	bestBits := maxBits
	ok := false
	for bits := uint8(vp8lMinColorCacheBits); bits <= vp8lMaxColorCacheBits; bits++ {
		_, literalAnalysis, greenCounts, cacheHits := vp8lBuildLZ77ColorCache(readPixel, bounds, mainWidth, lz77Tokens, bits, false, base.analysis)
		if cacheHits == 0 {
			continue
		}
		greenLimit := nLiteralCodes + nLengthCodes + 1<<bits
		greenLengths, lengthsOK := huffmanColorCacheCodeLengths(greenCounts[:greenLimit])
		if !lengthsOK {
			continue
		}
		candidate := base
		candidate.colorCache = &vp8lColorCachePlan{
			bits:     bits,
			analysis: literalAnalysis,
			counts:   greenCounts,
			lengths:  greenLengths,
			codes:    canonicalColorCacheCodes(greenLengths),
		}
		candidateBits := vp8lPayloadBits(width, height, candidate)
		if candidateBits >= bestBits {
			continue
		}
		tokens, _, _, _ := vp8lBuildLZ77ColorCache(readPixel, bounds, mainWidth, lz77Tokens, bits, true, base.analysis)
		candidate.colorCache.tokens = tokens
		best = candidate
		bestBits = candidateBits
		ok = true
	}
	return best, ok
}

func vp8lBuildLZ77ColorCache(readPixel pixelReader, bounds image.Rectangle, width int, lz77Tokens []vp8lToken, bits uint8, collectTokens bool, baseAnalysis imageAnalysis) ([]vp8lToken, imageAnalysis, [nColorCacheGreenCodes]uint32, int) {
	var tokens []vp8lToken
	if collectTokens {
		tokens = make([]vp8lToken, 0, len(lz77Tokens))
	}
	literalObserver := newVP8LLiteralAnalysisObserver(baseAnalysis)
	var greenCounts [nColorCacheGreenCodes]uint32
	var cache [vp8lMaxColorCacheSize]color.NRGBA
	firstLiteral := true
	cacheHits := 0
	pos := 0

	emitLiteral := func(pos int) {
		pixel := vp8lPixelAt(readPixel, bounds, width, pos)
		index := vp8lColorCacheIndex(pixel, bits)
		if cache[index] == pixel {
			if collectTokens {
				tokens = append(tokens, vp8lToken{cacheIndex: index, colorCache: true})
			}
			greenCounts[nLiteralCodes+nLengthCodes+index]++
			cacheHits++
			return
		}
		if collectTokens {
			tokens = append(tokens, vp8lToken{pixel: pixel})
		}
		firstLiteral = false
		literalObserver.observePixel(pixel)
		greenCounts[pixel.G]++
		cache[index] = pixel
	}

	emitCopy := func(pos int, length int, distanceCode int) {
		lengthPrefix := vp8lPrefixCode(length)
		if collectTokens {
			tokens = append(tokens, vp8lToken{copyLength: length, distanceCode: distanceCode})
		}
		greenCounts[nLiteralCodes+lengthPrefix.code]++
		for i := 0; i < length; i++ {
			pixel := vp8lPixelAt(readPixel, bounds, width, pos+i)
			cache[vp8lColorCacheIndex(pixel, bits)] = pixel
		}
	}

	for _, token := range lz77Tokens {
		if token.copyLength > 0 {
			emitCopy(pos, token.copyLength, token.distanceCode)
			pos += token.copyLength
			continue
		}
		emitLiteral(pos)
		pos++
	}
	if firstLiteral {
		return tokens, emptyVP8LLiteralAnalysis(), greenCounts, cacheHits
	}
	return tokens, literalObserver.result(), greenCounts, cacheHits
}

type vp8lHashCandidateList [vp8lMaxHashCandidates]int32

func vp8lInitHashTables(primary *[vp8lHashSize][vp8lMinHashCandidates]int32, extra *[vp8lHashSize][vp8lMinHashCandidates]int32, candidateCount int) {
	for i := range primary {
		for j := range primary[i] {
			primary[i][j] = -1
		}
	}
	extraCount := candidateCount - vp8lMinHashCandidates
	if extraCount <= 0 {
		return
	}
	for i := range extra {
		for j := 0; j < extraCount; j++ {
			extra[i][j] = -1
		}
	}
}

func vp8lHashCandidateCount(total int) int {
	switch {
	case total <= 128*128:
		return vp8lMaxHashCandidates
	case total <= 512*512:
		return vp8lMidHashCandidates
	default:
		return vp8lMinHashCandidates
	}
}

func vp8lHashCandidatesFor(primary [vp8lMinHashCandidates]int32, extra [vp8lMinHashCandidates]int32, candidateCount int) vp8lHashCandidateList {
	var candidates vp8lHashCandidateList
	copy(candidates[:vp8lMinHashCandidates], primary[:])
	if candidateCount > vp8lMinHashCandidates {
		copy(candidates[vp8lMinHashCandidates:candidateCount], extra[:candidateCount-vp8lMinHashCandidates])
	}
	return candidates
}

func vp8lInsertHash(primary *[vp8lHashSize][vp8lMinHashCandidates]int32, extra *[vp8lHashSize][vp8lMinHashCandidates]int32, candidateCount int, readPixel pixelReader, bounds image.Rectangle, width int, pos int, total int) {
	if pos+vp8lMinBackwardRefLength > total {
		return
	}
	hash := vp8lHashAt(readPixel, bounds, width, pos)
	primaryBucket := &primary[hash]
	if candidateCount > vp8lMinHashCandidates {
		extraBucket := &extra[hash]
		extraCount := candidateCount - vp8lMinHashCandidates
		copy(extraBucket[1:extraCount], extraBucket[:extraCount-1])
		extraBucket[0] = primaryBucket[len(primaryBucket)-1]
	}
	copy(primaryBucket[1:], primaryBucket[:len(primaryBucket)-1])
	primaryBucket[0] = int32(pos)
}

func vp8lNextLazyMatch(primary *[vp8lHashSize][vp8lMinHashCandidates]int32, extra *[vp8lHashSize][vp8lMinHashCandidates]int32, candidateCount int, currentHash int, readPixel pixelReader, bounds image.Rectangle, width int, pos int, total int) vp8lMatch {
	nextPos := pos + 1
	if nextPos+vp8lMinBackwardRefLength > total {
		return vp8lMatch{}
	}
	nextHash := vp8lHashAt(readPixel, bounds, width, nextPos)
	nextCandidates := vp8lHashCandidatesFor(primary[nextHash], extra[nextHash], candidateCount)
	if nextHash == currentHash {
		currentCandidates := vp8lHashCandidatesFor(primary[currentHash], extra[currentHash], candidateCount)
		copy(nextCandidates[1:candidateCount], currentCandidates[:candidateCount-1])
		nextCandidates[0] = int32(pos)
	}
	return vp8lBestHashMatch(nextCandidates, candidateCount, readPixel, bounds, width, nextPos, total)
}

func vp8lShouldUseLazyMatch(current vp8lMatch, next vp8lMatch) bool {
	return next.length >= vp8lMinBackwardRefLength && next.length >= current.length+vp8lLazyMatchMinGain
}

func vp8lBestHashMatch(candidates vp8lHashCandidateList, candidateCount int, readPixel pixelReader, bounds image.Rectangle, width int, pos int, total int) vp8lMatch {
	var best vp8lMatch
	maxLength := total - pos
	if maxLength > vp8lMaxBackwardRefLength {
		maxLength = vp8lMaxBackwardRefLength
	}
	for i := 0; i < candidateCount; i++ {
		matchPos := int(candidates[i])
		if matchPos < 0 || matchPos >= pos {
			continue
		}
		distance := pos - matchPos
		distanceCode, ok := vp8lDistanceCodeForPositionDistance(distance, width)
		if !ok {
			continue
		}
		length := vp8lMatchLength(readPixel, bounds, width, matchPos, pos, total)
		if length < vp8lMinBackwardRefLength {
			continue
		}
		if length > best.length || length == best.length && distance < best.distance {
			best = vp8lMatch{
				length:       length,
				distance:     distance,
				distanceCode: distanceCode,
			}
			if best.length == maxLength {
				return best
			}
		}
	}
	return best
}

func vp8lMatchLength(readPixel pixelReader, bounds image.Rectangle, width int, matchPos int, pos int, total int) int {
	maxLength := total - pos
	if maxLength > vp8lMaxBackwardRefLength {
		maxLength = vp8lMaxBackwardRefLength
	}
	length := 0
	for length < maxLength {
		if vp8lPixelAt(readPixel, bounds, width, matchPos+length) != vp8lPixelAt(readPixel, bounds, width, pos+length) {
			break
		}
		length++
	}
	return length
}

func vp8lDistanceCodeForPositionDistance(distance int, width int) (int, bool) {
	if distance <= 0 {
		return 0, false
	}
	if code, ok := vp8lSpecialDistanceCode(distance, width); ok {
		return code, true
	}
	distanceCode := distance + 120
	if distanceCode > vp8lMaxDistanceCode {
		return 0, false
	}
	return distanceCode, true
}

func vp8lSpecialDistanceCode(distance int, width int) (int, bool) {
	if width >= 16 {
		return vp8lSpecialDistanceCodeFast(distance, width)
	}
	for i, offset := range vp8lDistanceMap {
		mapped := offset.x + offset.y*width
		if mapped == distance && mapped >= 1 {
			return i + 1, true
		}
	}
	return 0, false
}

func vp8lSpecialDistanceCodeFast(distance int, width int) (int, bool) {
	y := distance / width
	x := distance - y*width
	if y < len(vp8lSpecialDistanceCodeByOffset) && x <= 8 {
		if code := vp8lSpecialDistanceCodeByOffset[y][x+7]; code != 0 {
			return int(code), true
		}
	}
	y++
	x -= width
	if y < len(vp8lSpecialDistanceCodeByOffset) && x >= -7 {
		if code := vp8lSpecialDistanceCodeByOffset[y][x+7]; code != 0 {
			return int(code), true
		}
	}
	return 0, false
}

func vp8lHashAt(readPixel pixelReader, bounds image.Rectangle, width int, pos int) int {
	return vp8lHashIndex(vp8lHashValueAt(readPixel, bounds, width, pos))
}

func vp8lHashIndex(hashValue uint32) int {
	return int((hashValue >> (32 - vp8lHashBits)) & (vp8lHashSize - 1))
}

func vp8lHashValueAt(readPixel pixelReader, bounds image.Rectangle, width int, pos int) uint32 {
	a := vp8lPixelAt(readPixel, bounds, width, pos)
	b := vp8lPixelAt(readPixel, bounds, width, pos+1)
	c := vp8lPixelAt(readPixel, bounds, width, pos+2)
	h := uint32(a.R)*0x1e35a7bd ^ uint32(a.G)*0x85ebca6b ^ uint32(a.B)*0xc2b2ae35 ^ uint32(a.A)*0x27d4eb2d
	h ^= uint32(b.R)<<3 ^ uint32(b.G)<<11 ^ uint32(b.B)<<19 ^ uint32(b.A)<<27
	h ^= uint32(c.R)*0x9e3779b1 ^ uint32(c.G)*0x7f4a7c15 ^ uint32(c.B)*0x94d049bb ^ uint32(c.A)*0x2545f491
	return h
}

func vp8lPixelAt(readPixel pixelReader, bounds image.Rectangle, width int, pos int) color.NRGBA {
	return readPixel(bounds.Min.X+pos%width, bounds.Min.Y+pos/width)
}

func nrgbaManhattanDistance(a color.NRGBA, b color.NRGBA) int {
	return absInt(int(a.R)-int(b.R)) + absInt(int(a.G)-int(b.G)) + absInt(int(a.B)-int(b.B)) + absInt(int(a.A)-int(b.A))
}

type vp8lDistanceOffset struct {
	x int
	y int
}

var vp8lDistanceMap = [...]vp8lDistanceOffset{
	{0, 1}, {1, 0}, {1, 1}, {-1, 1}, {0, 2}, {2, 0}, {1, 2}, {-1, 2},
	{2, 1}, {-2, 1}, {2, 2}, {-2, 2}, {0, 3}, {3, 0}, {1, 3}, {-1, 3},
	{3, 1}, {-3, 1}, {2, 3}, {-2, 3}, {3, 2}, {-3, 2}, {0, 4}, {4, 0},
	{1, 4}, {-1, 4}, {4, 1}, {-4, 1}, {3, 3}, {-3, 3}, {2, 4}, {-2, 4},
	{4, 2}, {-4, 2}, {0, 5}, {3, 4}, {-3, 4}, {4, 3}, {-4, 3}, {5, 0},
	{1, 5}, {-1, 5}, {5, 1}, {-5, 1}, {2, 5}, {-2, 5}, {5, 2}, {-5, 2},
	{4, 4}, {-4, 4}, {3, 5}, {-3, 5}, {5, 3}, {-5, 3}, {0, 6}, {6, 0},
	{1, 6}, {-1, 6}, {6, 1}, {-6, 1}, {2, 6}, {-2, 6}, {6, 2}, {-6, 2},
	{4, 5}, {-4, 5}, {5, 4}, {-5, 4}, {3, 6}, {-3, 6}, {6, 3}, {-6, 3},
	{0, 7}, {7, 0}, {1, 7}, {-1, 7}, {5, 5}, {-5, 5}, {7, 1}, {-7, 1},
	{4, 6}, {-4, 6}, {6, 4}, {-6, 4}, {2, 7}, {-2, 7}, {7, 2}, {-7, 2},
	{3, 7}, {-3, 7}, {7, 3}, {-7, 3}, {5, 6}, {-5, 6}, {6, 5}, {-6, 5},
	{8, 0}, {4, 7}, {-4, 7}, {7, 4}, {-7, 4}, {8, 1}, {8, 2}, {6, 6},
	{-6, 6}, {8, 3}, {5, 7}, {-5, 7}, {7, 5}, {-7, 5}, {8, 4}, {6, 7},
	{-6, 7}, {7, 6}, {-7, 6}, {8, 5}, {7, 7}, {-7, 7}, {8, 6}, {8, 7},
}

var vp8lSpecialDistanceCodeByOffset = func() [8][16]uint8 {
	var table [8][16]uint8
	for i, offset := range vp8lDistanceMap {
		if offset.y < 0 || offset.y >= len(table) || offset.x < -7 || offset.x > 8 {
			continue
		}
		table[offset.y][offset.x+7] = uint8(i + 1)
	}
	return table
}()

func observeVP8LLiteral(analysis *imageAnalysis, first *bool, pixel color.NRGBA) {
	if *first {
		analysis.channels[0] = newConstantChannelPlan(pixel.G)
		analysis.channels[1] = newConstantChannelPlan(pixel.R)
		analysis.channels[2] = newConstantChannelPlan(pixel.B)
		analysis.channels[3] = newConstantChannelPlan(pixel.A)
		*first = false
	} else {
		analysis.channels[0].observe(pixel.G)
		analysis.channels[1].observe(pixel.R)
		analysis.channels[2].observe(pixel.B)
		analysis.channels[3].observe(pixel.A)
	}
	analysis.alpha = analysis.alpha || pixel.A != 255
}

func vp8lPlanPixelReader(readPixel pixelReader, bounds image.Rectangle, width int, height int, plan vp8lEncodingPlan) pixelReader {
	if plan.colorIndexing {
		var readIndexed pixelReader
		if plan.colorIndexReader != nil {
			readIndexed = plan.colorIndexReader
		} else {
			readIndexed = vp8lColorIndexedImageReader(readPixel, bounds, width, plan.colorIndexWidthBits, plan.colorIndex)
		}
		if !plan.predictor {
			return readIndexed
		}
		indexedWidth, indexedHeight := vp8lPlanImageDimensions(width, height, plan)
		indexedBounds := image.Rect(0, 0, indexedWidth, indexedHeight)
		if len(plan.predictorImage) == 0 {
			return vp8lPredictorResidualReader(readIndexed, indexedBounds, indexedWidth, indexedHeight, plan.predictorMode)
		}
		transformWidth, _ := vp8lTransformDimensions(indexedWidth, indexedHeight, plan.predictorSizeBits)
		return vp8lBlockPredictorResidualReader(readIndexed, indexedBounds, indexedWidth, indexedHeight, plan.predictorSizeBits, plan.predictorImage, transformWidth)
	}
	read := readPixel
	if plan.predictor {
		if len(plan.predictorImage) == 0 {
			read = vp8lPredictorResidualReader(read, bounds, width, height, plan.predictorMode)
		} else {
			transformWidth, _ := vp8lTransformDimensions(width, height, plan.predictorSizeBits)
			read = vp8lBlockPredictorResidualReader(read, bounds, width, height, plan.predictorSizeBits, plan.predictorImage, transformWidth)
		}
	}
	if plan.colorTransform {
		read = vp8lColorTransformReader(read, plan.colorElement)
	}
	if plan.subtractGreen {
		read = vp8lSubtractGreenReader(read)
	}
	return read
}

func vp8lPredictorResidualReader(readPixel pixelReader, bounds image.Rectangle, width int, height int, mode uint8) pixelReader {
	return func(x int, y int) color.NRGBA {
		c := readPixel(x, y)
		pred := vp8lPredictorPixel(readPixel, bounds, width, height, x, y, mode)
		return subtractNRGBA(c, pred)
	}
}

func vp8lBlockPredictorResidualReader(readPixel pixelReader, bounds image.Rectangle, width int, height int, sizeBits uint8, predictorImage []uint8, predictorWidth int) pixelReader {
	return func(x int, y int) color.NRGBA {
		blockX := (x - bounds.Min.X) >> sizeBits
		blockY := (y - bounds.Min.Y) >> sizeBits
		mode := predictorImage[blockY*predictorWidth+blockX]
		c := readPixel(x, y)
		pred := vp8lPredictorPixel(readPixel, bounds, width, height, x, y, mode)
		return subtractNRGBA(c, pred)
	}
}

func vp8lSubtractGreenReader(readPixel pixelReader) pixelReader {
	return func(x int, y int) color.NRGBA {
		c := readPixel(x, y)
		return subtractGreenFromNRGBA(c)
	}
}

func subtractGreenFromNRGBA(c color.NRGBA) color.NRGBA {
	return color.NRGBA{
		R: c.R - c.G,
		G: c.G,
		B: c.B - c.G,
		A: c.A,
	}
}

func vp8lColorTransformReader(readPixel pixelReader, element vp8lColorTransformElement) pixelReader {
	return func(x int, y int) color.NRGBA {
		c := readPixel(x, y)
		return applyVP8LColorTransform(c, element)
	}
}

func applyVP8LColorTransform(c color.NRGBA, element vp8lColorTransformElement) color.NRGBA {
	red := c.R - colorTransformDelta(element.greenToRed, c.G)
	blue := c.B - colorTransformDelta(element.greenToBlue, c.G) - colorTransformDelta(element.redToBlue, c.R)
	return color.NRGBA{
		R: red,
		G: c.G,
		B: blue,
		A: c.A,
	}
}

func inverseVP8LColorTransform(c color.NRGBA, element vp8lColorTransformElement) color.NRGBA {
	red := c.R + colorTransformDelta(element.greenToRed, c.G)
	blue := c.B + colorTransformDelta(element.greenToBlue, c.G) + colorTransformDelta(element.redToBlue, red)
	return color.NRGBA{
		R: red,
		G: c.G,
		B: blue,
		A: c.A,
	}
}

func colorTransformDelta(t uint8, c uint8) uint8 {
	return uint8((int(int8(t)) * int(int8(c))) >> 5)
}

func vp8lTransformDimensions(width int, height int, sizeBits uint8) (int, int) {
	blockSize := 1 << sizeBits
	return divRoundUp(width, blockSize), divRoundUp(height, blockSize)
}

func divRoundUp(n int, d int) int {
	return (n + d - 1) / d
}

func vp8lPredictorPixel(readPixel pixelReader, bounds image.Rectangle, width int, height int, x int, y int, mode uint8) color.NRGBA {
	localX := x - bounds.Min.X
	localY := y - bounds.Min.Y
	if localX == 0 && localY == 0 {
		return color.NRGBA{A: 255}
	}
	if localY == 0 {
		return readPixel(x-1, y)
	}
	if localX == 0 {
		return readPixel(x, y-1)
	}

	left := readPixel(x-1, y)
	top := readPixel(x, y-1)
	topLeft := readPixel(x-1, y-1)
	topRightX := x + 1
	topRightY := y - 1
	if localX == width-1 {
		topRightX = bounds.Min.X
		topRightY = y
	}
	topRight := readPixel(topRightX, topRightY)
	return vp8lPredictorFromNeighbors(mode, left, top, topRight, topLeft)
}

func vp8lPredictorFromNeighbors(mode uint8, left color.NRGBA, top color.NRGBA, topRight color.NRGBA, topLeft color.NRGBA) color.NRGBA {
	switch mode {
	case 0:
		return color.NRGBA{A: 255}
	case 1:
		return left
	case 2:
		return top
	case 3:
		return topRight
	case 4:
		return topLeft
	case 5:
		return averageNRGBA(averageNRGBA(left, topRight), top)
	case 6:
		return averageNRGBA(left, topLeft)
	case 7:
		return averageNRGBA(left, top)
	case 8:
		return averageNRGBA(topLeft, top)
	case 9:
		return averageNRGBA(top, topRight)
	case 10:
		return averageNRGBA(averageNRGBA(left, topLeft), averageNRGBA(top, topRight))
	case 11:
		return selectPredictorNRGBA(left, top, topLeft)
	case 12:
		return clampAddSubtractFullNRGBA(left, top, topLeft)
	case 13:
		return clampAddSubtractHalfNRGBA(averageNRGBA(left, top), topLeft)
	default:
		return color.NRGBA{A: 255}
	}
}

func subtractNRGBA(a color.NRGBA, b color.NRGBA) color.NRGBA {
	return color.NRGBA{
		R: a.R - b.R,
		G: a.G - b.G,
		B: a.B - b.B,
		A: a.A - b.A,
	}
}

func averageNRGBA(a color.NRGBA, b color.NRGBA) color.NRGBA {
	return color.NRGBA{
		R: averageUint8(a.R, b.R),
		G: averageUint8(a.G, b.G),
		B: averageUint8(a.B, b.B),
		A: averageUint8(a.A, b.A),
	}
}

func averageUint8(a uint8, b uint8) uint8 {
	return uint8((uint16(a) + uint16(b)) / 2)
}

func selectPredictorNRGBA(left color.NRGBA, top color.NRGBA, topLeft color.NRGBA) color.NRGBA {
	pAlpha := int(left.A) + int(top.A) - int(topLeft.A)
	pRed := int(left.R) + int(top.R) - int(topLeft.R)
	pGreen := int(left.G) + int(top.G) - int(topLeft.G)
	pBlue := int(left.B) + int(top.B) - int(topLeft.B)
	pLeft := absInt(pAlpha-int(left.A)) + absInt(pRed-int(left.R)) + absInt(pGreen-int(left.G)) + absInt(pBlue-int(left.B))
	pTop := absInt(pAlpha-int(top.A)) + absInt(pRed-int(top.R)) + absInt(pGreen-int(top.G)) + absInt(pBlue-int(top.B))
	if pLeft < pTop {
		return left
	}
	return top
}

func clampAddSubtractFullNRGBA(a color.NRGBA, b color.NRGBA, c color.NRGBA) color.NRGBA {
	return color.NRGBA{
		R: clampUint8(int(a.R) + int(b.R) - int(c.R)),
		G: clampUint8(int(a.G) + int(b.G) - int(c.G)),
		B: clampUint8(int(a.B) + int(b.B) - int(c.B)),
		A: clampUint8(int(a.A) + int(b.A) - int(c.A)),
	}
}

func clampAddSubtractHalfNRGBA(a color.NRGBA, b color.NRGBA) color.NRGBA {
	return color.NRGBA{
		R: clampUint8(int(a.R) + (int(a.R)-int(b.R))/2),
		G: clampUint8(int(a.G) + (int(a.G)-int(b.G))/2),
		B: clampUint8(int(a.B) + (int(a.B)-int(b.B))/2),
		A: clampUint8(int(a.A) + (int(a.A)-int(b.A))/2),
	}
}

func clampUint8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func writeVP8LMetaPrefixImageData(bits *bitWriter, readPixel pixelReader, bounds image.Rectangle, plan vp8lEncodingPlan) {
	metaPrefix := plan.metaPrefix
	bits.writeBits(0, 1) // no color cache
	bits.writeBits(1, 1) // meta prefix image present
	bits.writeBits(uint32(metaPrefix.prefixBits-vp8lMinMetaPrefixBits), 3)

	prefixBounds := image.Rect(0, 0, metaPrefix.width, metaPrefix.height)
	writeVP8LImageData(bits, vp8lMetaPrefixImageReader(metaPrefix.image, metaPrefix.width), prefixBounds, metaPrefix.imageAnalysis, false)

	for _, group := range metaPrefix.groups {
		writeChannelTree(bits, group.channels[0], nLiteralCodes+nLengthCodes)
		writeChannelTree(bits, group.channels[1], nLiteralCodes)
		writeChannelTree(bits, group.channels[2], nLiteralCodes)
		writeChannelTree(bits, group.channels[3], nLiteralCodes)
		writeSimpleTree(bits, 0)
	}

	groupUseNormal := make([][4]bool, len(metaPrefix.groups))
	for i, group := range metaPrefix.groups {
		groupUseNormal[i] = [4]bool{
			channelUseNormal(group.channels[0], nLiteralCodes+nLengthCodes),
			channelUseNormal(group.channels[1], nLiteralCodes),
			channelUseNormal(group.channels[2], nLiteralCodes),
			channelUseNormal(group.channels[3], nLiteralCodes),
		}
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			localX := x - bounds.Min.X
			localY := y - bounds.Min.Y
			groupIndex := vp8lMetaPrefixGroupAt(metaPrefix, localX, localY)
			group := metaPrefix.groups[groupIndex]
			useNormal := groupUseNormal[groupIndex]
			c := readPixel(x, y)
			writeChannelSymbolSelected(bits, group.channels[0], useNormal[0], c.G)
			writeChannelSymbolSelected(bits, group.channels[1], useNormal[1], c.R)
			writeChannelSymbolSelected(bits, group.channels[2], useNormal[2], c.B)
			writeChannelSymbolSelected(bits, group.channels[3], useNormal[3], c.A)
		}
	}
}

func writeVP8LMetaPrefixLZ77ImageData(bits *bitWriter, readPixel pixelReader, bounds image.Rectangle, width int, plan vp8lEncodingPlan) {
	metaPrefix := plan.metaPrefix
	bits.writeBits(0, 1) // no color cache
	bits.writeBits(1, 1) // meta prefix image present
	bits.writeBits(uint32(metaPrefix.prefixBits-vp8lMinMetaPrefixBits), 3)

	prefixBounds := image.Rect(0, 0, metaPrefix.width, metaPrefix.height)
	writeVP8LImageData(bits, vp8lMetaPrefixImageReader(metaPrefix.image, metaPrefix.width), prefixBounds, metaPrefix.imageAnalysis, false)

	for _, group := range metaPrefix.lz77Groups {
		writeAlphaNormalTree(bits, group.greenLengths[:])
		writeChannelTree(bits, group.literalAnalysis.channels[1], nLiteralCodes)
		writeChannelTree(bits, group.literalAnalysis.channels[2], nLiteralCodes)
		writeChannelTree(bits, group.literalAnalysis.channels[3], nLiteralCodes)
		writeVP8LLZ77GroupDistanceTree(bits, group)
	}

	groupUseNormal := make([][4]bool, len(metaPrefix.lz77Groups))
	for i, group := range metaPrefix.lz77Groups {
		groupUseNormal[i] = [4]bool{
			false,
			channelUseNormal(group.literalAnalysis.channels[1], nLiteralCodes),
			channelUseNormal(group.literalAnalysis.channels[2], nLiteralCodes),
			channelUseNormal(group.literalAnalysis.channels[3], nLiteralCodes),
		}
	}
	pos := 0
	writeToken := func(token vp8lToken) {
		x := pos % width
		y := pos / width
		groupIndex := vp8lMetaPrefixGroupAt(metaPrefix, x, y)
		group := metaPrefix.lz77Groups[groupIndex]
		useNormal := groupUseNormal[groupIndex]
		if token.copyLength > 0 {
			prefix := vp8lPrefixCode(token.copyLength)
			distancePrefix := vp8lDistancePrefixCode(token.distanceCode)
			writeVP8LHuffmanSymbol(bits, group.greenCodes[:], group.greenLengths[:], nLiteralCodes+prefix.code)
			bits.writeBits(prefix.extra, prefix.extraBits)
			writeVP8LLZ77GroupDistanceSymbol(bits, group, distancePrefix.code)
			bits.writeBits(distancePrefix.extra, distancePrefix.extraBits)
			pos += token.copyLength
			return
		}
		writeVP8LHuffmanSymbol(bits, group.greenCodes[:], group.greenLengths[:], int(token.pixel.G))
		writeChannelSymbolSelected(bits, group.literalAnalysis.channels[1], useNormal[1], token.pixel.R)
		writeChannelSymbolSelected(bits, group.literalAnalysis.channels[2], useNormal[2], token.pixel.B)
		writeChannelSymbolSelected(bits, group.literalAnalysis.channels[3], useNormal[3], token.pixel.A)
		pos++
	}
	if plan.lz77Tokens != nil {
		for _, token := range plan.lz77Tokens {
			writeToken(token)
		}
		return
	}
	writeVP8LLZ77GeneratedTokens(readPixel, bounds, width, writeToken)
}

func writeVP8LMetaPrefixColorCacheImageData(bits *bitWriter, width int, plan vp8lEncodingPlan) {
	metaPrefix := plan.metaPrefix
	colorCache := plan.colorCache
	bits.writeBits(1, 1)
	bits.writeBits(uint32(colorCache.bits), 4)
	bits.writeBits(1, 1) // meta prefix image present
	bits.writeBits(uint32(metaPrefix.prefixBits-vp8lMinMetaPrefixBits), 3)

	prefixBounds := image.Rect(0, 0, metaPrefix.width, metaPrefix.height)
	writeVP8LImageData(bits, vp8lMetaPrefixImageReader(metaPrefix.image, metaPrefix.width), prefixBounds, metaPrefix.imageAnalysis, false)

	greenLimit := nLiteralCodes + nLengthCodes + 1<<colorCache.bits
	for _, group := range metaPrefix.colorCacheGroups {
		writeAlphaNormalTree(bits, group.lengths[:greenLimit])
		writeChannelTree(bits, group.literalAnalysis.channels[1], nLiteralCodes)
		writeChannelTree(bits, group.literalAnalysis.channels[2], nLiteralCodes)
		writeChannelTree(bits, group.literalAnalysis.channels[3], nLiteralCodes)
		writeSimpleTree(bits, 0)
	}

	groupUseNormal := make([][4]bool, len(metaPrefix.colorCacheGroups))
	for i, group := range metaPrefix.colorCacheGroups {
		groupUseNormal[i] = [4]bool{
			false,
			channelUseNormal(group.literalAnalysis.channels[1], nLiteralCodes),
			channelUseNormal(group.literalAnalysis.channels[2], nLiteralCodes),
			channelUseNormal(group.literalAnalysis.channels[3], nLiteralCodes),
		}
	}
	for pos, token := range colorCache.tokens {
		groupIndex := vp8lMetaPrefixGroupAt(metaPrefix, pos%width, pos/width)
		group := metaPrefix.colorCacheGroups[groupIndex]
		useNormal := groupUseNormal[groupIndex]
		if token.colorCache {
			writeVP8LHuffmanSymbol(bits, group.codes[:greenLimit], group.lengths[:greenLimit], nLiteralCodes+nLengthCodes+token.cacheIndex)
			continue
		}
		writeVP8LHuffmanSymbol(bits, group.codes[:greenLimit], group.lengths[:greenLimit], int(token.pixel.G))
		writeChannelSymbolSelected(bits, group.literalAnalysis.channels[1], useNormal[1], token.pixel.R)
		writeChannelSymbolSelected(bits, group.literalAnalysis.channels[2], useNormal[2], token.pixel.B)
		writeChannelSymbolSelected(bits, group.literalAnalysis.channels[3], useNormal[3], token.pixel.A)
	}
}

func writeVP8LImageData(bits *bitWriter, readPixel pixelReader, bounds image.Rectangle, analysis imageAnalysis, metaPrefix bool) {
	bits.writeBits(0, 1) // no color cache
	if metaPrefix {
		bits.writeBits(0, 1) // no meta prefix image
	}

	writeChannelTree(bits, analysis.channels[0], nLiteralCodes+nLengthCodes)
	writeChannelTree(bits, analysis.channels[1], nLiteralCodes)
	writeChannelTree(bits, analysis.channels[2], nLiteralCodes)
	writeChannelTree(bits, analysis.channels[3], nLiteralCodes)
	writeSimpleTree(bits, 0)

	if analysis.allChannelsConstant() {
		return
	}
	useNormal := [4]bool{
		channelUseNormal(analysis.channels[0], nLiteralCodes+nLengthCodes),
		channelUseNormal(analysis.channels[1], nLiteralCodes),
		channelUseNormal(analysis.channels[2], nLiteralCodes),
		channelUseNormal(analysis.channels[3], nLiteralCodes),
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			c := readPixel(x, y)
			writeChannelSymbolSelected(bits, analysis.channels[0], useNormal[0], c.G)
			writeChannelSymbolSelected(bits, analysis.channels[1], useNormal[1], c.R)
			writeChannelSymbolSelected(bits, analysis.channels[2], useNormal[2], c.B)
			writeChannelSymbolSelected(bits, analysis.channels[3], useNormal[3], c.A)
		}
	}
}

func writeVP8LColorCacheImageData(bits *bitWriter, plan vp8lEncodingPlan, metaPrefix bool) {
	colorCache := plan.colorCache
	bits.writeBits(1, 1)
	bits.writeBits(uint32(colorCache.bits), 4)
	if metaPrefix {
		bits.writeBits(0, 1) // no meta prefix image
	}
	greenLimit := nLiteralCodes + nLengthCodes + 1<<colorCache.bits
	writeAlphaNormalTree(bits, colorCache.lengths[:greenLimit])
	writeChannelTree(bits, colorCache.analysis.channels[1], nLiteralCodes)
	writeChannelTree(bits, colorCache.analysis.channels[2], nLiteralCodes)
	writeChannelTree(bits, colorCache.analysis.channels[3], nLiteralCodes)
	if plan.lz77 {
		writeVP8LDistanceTree(bits, plan)
	} else {
		writeSimpleTree(bits, 0)
	}

	useNormal := [4]bool{
		false,
		channelUseNormal(colorCache.analysis.channels[1], nLiteralCodes),
		channelUseNormal(colorCache.analysis.channels[2], nLiteralCodes),
		channelUseNormal(colorCache.analysis.channels[3], nLiteralCodes),
	}
	for _, token := range colorCache.tokens {
		if token.copyLength > 0 {
			prefix := vp8lPrefixCode(token.copyLength)
			distancePrefix := vp8lDistancePrefixCode(token.distanceCode)
			writeVP8LHuffmanSymbol(bits, colorCache.codes[:greenLimit], colorCache.lengths[:greenLimit], nLiteralCodes+prefix.code)
			bits.writeBits(prefix.extra, prefix.extraBits)
			writeVP8LDistanceSymbol(bits, plan, distancePrefix.code)
			bits.writeBits(distancePrefix.extra, distancePrefix.extraBits)
			continue
		}
		if token.colorCache {
			writeVP8LHuffmanSymbol(bits, colorCache.codes[:greenLimit], colorCache.lengths[:greenLimit], nLiteralCodes+nLengthCodes+token.cacheIndex)
			continue
		}
		writeVP8LHuffmanSymbol(bits, colorCache.codes[:greenLimit], colorCache.lengths[:greenLimit], int(token.pixel.G))
		writeChannelSymbolSelected(bits, colorCache.analysis.channels[1], useNormal[1], token.pixel.R)
		writeChannelSymbolSelected(bits, colorCache.analysis.channels[2], useNormal[2], token.pixel.B)
		writeChannelSymbolSelected(bits, colorCache.analysis.channels[3], useNormal[3], token.pixel.A)
	}
}

func writeVP8LLZ77ImageData(bits *bitWriter, readPixel pixelReader, bounds image.Rectangle, width int, plan vp8lEncodingPlan, metaPrefix bool) {
	bits.writeBits(0, 1) // no color cache
	if metaPrefix {
		bits.writeBits(0, 1) // no meta prefix image
	}

	writeAlphaNormalTree(bits, plan.lz77GreenLengths[:])
	writeChannelTree(bits, plan.lz77LiteralAnalysis.channels[1], nLiteralCodes)
	writeChannelTree(bits, plan.lz77LiteralAnalysis.channels[2], nLiteralCodes)
	writeChannelTree(bits, plan.lz77LiteralAnalysis.channels[3], nLiteralCodes)
	writeVP8LDistanceTree(bits, plan)

	useNormal := [4]bool{
		false,
		channelUseNormal(plan.lz77LiteralAnalysis.channels[1], nLiteralCodes),
		channelUseNormal(plan.lz77LiteralAnalysis.channels[2], nLiteralCodes),
		channelUseNormal(plan.lz77LiteralAnalysis.channels[3], nLiteralCodes),
	}
	writeToken := func(token vp8lToken) {
		if token.copyLength > 0 {
			prefix := vp8lPrefixCode(token.copyLength)
			distancePrefix := vp8lDistancePrefixCode(token.distanceCode)
			writeVP8LHuffmanSymbol(bits, plan.lz77GreenCodes[:], plan.lz77GreenLengths[:], nLiteralCodes+prefix.code)
			bits.writeBits(prefix.extra, prefix.extraBits)
			writeVP8LDistanceSymbol(bits, plan, distancePrefix.code)
			bits.writeBits(distancePrefix.extra, distancePrefix.extraBits)
			return
		}
		writeVP8LHuffmanSymbol(bits, plan.lz77GreenCodes[:], plan.lz77GreenLengths[:], int(token.pixel.G))
		writeChannelSymbolSelected(bits, plan.lz77LiteralAnalysis.channels[1], useNormal[1], token.pixel.R)
		writeChannelSymbolSelected(bits, plan.lz77LiteralAnalysis.channels[2], useNormal[2], token.pixel.B)
		writeChannelSymbolSelected(bits, plan.lz77LiteralAnalysis.channels[3], useNormal[3], token.pixel.A)
	}
	if plan.lz77Tokens != nil {
		for _, token := range plan.lz77Tokens {
			writeToken(token)
		}
		return
	}
	writeVP8LLZ77GeneratedTokens(readPixel, bounds, width, writeToken)
}

func writeVP8LLZ77GeneratedTokens(readPixel pixelReader, bounds image.Rectangle, width int, writeToken func(vp8lToken)) {
	total := bounds.Dx() * bounds.Dy()
	candidateCount := vp8lHashCandidateCount(total)
	var primaryHashTable [vp8lHashSize][vp8lMinHashCandidates]int32
	var extraHashTable [vp8lHashSize][vp8lMinHashCandidates]int32
	vp8lInitHashTables(&primaryHashTable, &extraHashTable, candidateCount)

	for pos := 0; pos < total; {
		if pos+vp8lMinBackwardRefLength <= total {
			hash := vp8lHashAt(readPixel, bounds, width, pos)
			candidates := vp8lHashCandidatesFor(primaryHashTable[hash], extraHashTable[hash], candidateCount)
			match := vp8lBestHashMatch(candidates, candidateCount, readPixel, bounds, width, pos, total)
			if match.length >= vp8lMinBackwardRefLength {
				nextMatch := vp8lNextLazyMatch(&primaryHashTable, &extraHashTable, candidateCount, hash, readPixel, bounds, width, pos, total)
				if vp8lShouldUseLazyMatch(match, nextMatch) {
					writeToken(vp8lToken{pixel: vp8lPixelAt(readPixel, bounds, width, pos)})
					vp8lInsertHash(&primaryHashTable, &extraHashTable, candidateCount, readPixel, bounds, width, pos, total)
					pos++
					continue
				}
				writeToken(vp8lToken{copyLength: match.length, distanceCode: match.distanceCode})
				for i := 0; i < match.length; i++ {
					vp8lInsertHash(&primaryHashTable, &extraHashTable, candidateCount, readPixel, bounds, width, pos+i, total)
				}
				pos += match.length
				continue
			}
			vp8lInsertHash(&primaryHashTable, &extraHashTable, candidateCount, readPixel, bounds, width, pos, total)
		}
		writeToken(vp8lToken{pixel: vp8lPixelAt(readPixel, bounds, width, pos)})
		pos++
	}
}

func writeVP8LDistanceTree(bits *bitWriter, plan vp8lEncodingPlan) {
	if plan.lz77DistanceNormal {
		writeAlphaNormalTree(bits, plan.lz77DistanceLengths[:])
		return
	}
	switch plan.lz77DistanceN {
	case 1:
		writeSimpleTree(bits, plan.lz77DistanceSymbols[0])
	case 2:
		writeTwoSymbolTree(bits, plan.lz77DistanceSymbols[0], plan.lz77DistanceSymbols[1])
	default:
		writeSimpleTree(bits, 0)
	}
}

func writeVP8LLZ77GroupDistanceTree(bits *bitWriter, group vp8lLZ77GroupPlan) {
	if group.distanceNormal {
		writeAlphaNormalTree(bits, group.distanceLengths[:])
		return
	}
	switch group.distanceN {
	case 1:
		writeSimpleTree(bits, group.distanceSymbols[0])
	case 2:
		writeTwoSymbolTree(bits, group.distanceSymbols[0], group.distanceSymbols[1])
	default:
		writeSimpleTree(bits, 0)
	}
}

func writeVP8LDistanceSymbol(bits *bitWriter, plan vp8lEncodingPlan, symbol int) {
	if plan.lz77DistanceNormal {
		writeVP8LHuffmanSymbol(bits, plan.lz77DistanceCodes[:], plan.lz77DistanceLengths[:], symbol)
		return
	}
	switch plan.lz77DistanceN {
	case 1:
		return
	case 2:
		if symbol == int(plan.lz77DistanceSymbols[0]) {
			bits.writeBits(0, 1)
		} else {
			bits.writeBits(1, 1)
		}
	}
}

func writeVP8LLZ77GroupDistanceSymbol(bits *bitWriter, group vp8lLZ77GroupPlan, symbol int) {
	if group.distanceNormal {
		writeVP8LHuffmanSymbol(bits, group.distanceCodes[:], group.distanceLengths[:], symbol)
		return
	}
	switch group.distanceN {
	case 1:
		return
	case 2:
		if symbol == int(group.distanceSymbols[0]) {
			bits.writeBits(0, 1)
		} else {
			bits.writeBits(1, 1)
		}
	}
}

func writeVP8LHuffmanSymbol(bits *bitWriter, codes []uint16, lengths []uint8, symbol int) {
	length := lengths[symbol]
	bits.writeBits(uint32(reverseBits(codes[symbol], length)), length)
}

func (a imageAnalysis) allChannelsConstant() bool {
	for _, ch := range a.channels {
		if !ch.constant {
			return false
		}
	}
	return true
}

func (a imageAnalysis) codingEqual(b imageAnalysis) bool {
	for i, ch := range a.channels {
		if !ch.codingEqual(b.channels[i]) {
			return false
		}
	}
	return true
}

func (p channelPlan) codingEqual(q channelPlan) bool {
	if p.constant != q.constant {
		return false
	}
	if p.constant {
		return p.value == q.value
	}
	if p.twoSymbol() != q.twoSymbol() {
		return false
	}
	if p.twoSymbol() {
		return p.symbols[0] == q.symbols[0] && p.symbols[1] == q.symbols[1]
	}
	if p.normal != q.normal {
		return false
	}
	if p.normal {
		if p.n != q.n {
			return false
		}
		for i := 0; i < p.n; i++ {
			if p.symbols[i] != q.symbols[i] || p.lengths[i] != q.lengths[i] {
				return false
			}
		}
		return true
	}
	return p.n < 0 && q.n < 0
}

func (a imageAnalysis) merge(b imageAnalysis) imageAnalysis {
	return imageAnalysis{
		channels: [4]channelPlan{
			a.channels[0].merge(b.channels[0]),
			a.channels[1].merge(b.channels[1]),
			a.channels[2].merge(b.channels[2]),
			a.channels[3].merge(b.channels[3]),
		},
		alpha: a.alpha || b.alpha,
	}
}

func (p channelPlan) merge(q channelPlan) channelPlan {
	if p.constant && q.constant && p.value == q.value {
		return p
	}
	if p.n < 0 || q.n < 0 {
		return channelPlan{n: -1}
	}
	var merged channelPlan
	for i := 0; i < p.n; i++ {
		merged.observeSymbolCount(p.symbols[i], p.counts[i])
	}
	for i := 0; i < q.n; i++ {
		merged.observeSymbolCount(q.symbols[i], q.counts[i])
	}
	merged.finalize()
	return merged
}

func (p channelPlan) twoSymbol() bool {
	return !p.constant && p.n == 2
}

func writeChannelTree(bits *bitWriter, ch channelPlan, alphabetSize int) {
	if ch.constant {
		writeSimpleTree(bits, ch.value)
		return
	}
	if ch.twoSymbol() {
		writeTwoSymbolTree(bits, ch.symbols[0], ch.symbols[1])
		return
	}
	if ch.normal && channelUseNormal(ch, alphabetSize) {
		writeChannelNormalTree(bits, ch, alphabetSize)
		return
	}
	writeFull8Tree(bits, alphabetSize)
}

func writeChannelNormalTree(bits *bitWriter, ch channelPlan, alphabetSize int) {
	var lengths [nColorCacheGreenCodes]uint8
	for i := 0; i < ch.n; i++ {
		lengths[ch.symbols[i]] = ch.lengths[i]
	}
	writeAlphaNormalTree(bits, lengths[:alphabetSize])
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

func writeChannelSymbol(bits *bitWriter, ch channelPlan, alphabetSize int, symbol uint8) {
	writeChannelSymbolSelected(bits, ch, channelUseNormal(ch, alphabetSize), symbol)
}

func writeChannelSymbolSelected(bits *bitWriter, ch channelPlan, useNormal bool, symbol uint8) {
	if ch.constant {
		return
	}
	if ch.twoSymbol() {
		if symbol == ch.symbols[0] {
			bits.writeBits(0, 1)
		} else {
			bits.writeBits(1, 1)
		}
		return
	}
	if ch.normal && useNormal {
		index := ch.smallSymbolIndex(symbol)
		length := ch.lengths[index]
		bits.writeBits(uint32(reverseBits(ch.codes[index], length)), length)
		return
	}
	bits.writeBits(uint32(reverse8(symbol)), 8)
}

func (p channelPlan) smallSymbolIndex(symbol uint8) int {
	for i := 0; i < p.n; i++ {
		if p.symbols[i] == symbol {
			return i
		}
	}
	return 0
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
