package webp

import (
	"image"
	"slices"
)

const vp8SegmentCount = 4

const vp8SegmentationMinMacroblocks = 8

type vp8SegmentConfig struct {
	quant       vp8Quant
	filterLevel int
	rd          vp8RDConfig
}

type vp8Segmentation struct {
	segments [vp8SegmentCount]vp8SegmentConfig
	mapIDs   []uint8
	mapProbs [3]uint8
	count    int

	activityLow  uint32
	activityHigh uint32
}

func (s *vp8Segmentation) enabled() bool {
	return s != nil && s.count > 1 && len(s.mapIDs) != 0
}

func (s *vp8Segmentation) quantForMacroblock(macroblock int, fallback vp8Quant) vp8Quant {
	if !s.enabled() || macroblock < 0 || macroblock >= len(s.mapIDs) {
		return fallback
	}
	id := s.mapIDs[macroblock]
	if id >= vp8SegmentCount {
		return fallback
	}
	quant := s.segments[id].quant
	quant.trellisProbs = fallback.trellisProbs
	quant.dcDiffusion = fallback.dcDiffusion
	return quant
}

func (s *vp8Segmentation) rdForMacroblock(macroblock int, fallback vp8RDConfig) vp8RDConfig {
	if !s.enabled() || macroblock < 0 || macroblock >= len(s.mapIDs) {
		return fallback
	}
	id := s.mapIDs[macroblock]
	if id >= vp8SegmentCount {
		return fallback
	}
	return s.segments[id].rd
}

func (s *vp8Segmentation) useDCDiffusion() bool {
	return s.enabled() && s.activityLow <= 16 && s.activityHigh > s.activityLow+16
}

func makeVP8Segmentation(readLuma lumaReader, bounds image.Rectangle, mbw int, mbh int, cfg vp8LossyConfig) vp8Segmentation {
	macroblocks := mbw * mbh
	if cfg.maxSegments < 2 || macroblocks < vp8SegmentationMinMacroblocks || cfg.qIndex <= 1 || cfg.qIndex >= 127 {
		return vp8Segmentation{}
	}

	activities := make([]uint32, macroblocks)
	for mby := 0; mby < mbh; mby++ {
		for mbx := 0; mbx < mbw; mbx++ {
			activities[mby*mbw+mbx] = vp8MacroblockActivity(readLuma, bounds, mbx, mby)
		}
	}
	return makeVP8SegmentationForActivities(activities, cfg)
}

func makeVP8SegmentationForActivities(activities []uint32, cfg vp8LossyConfig) vp8Segmentation {
	macroblocks := len(activities)
	if cfg.maxSegments < 2 || macroblocks < vp8SegmentationMinMacroblocks || cfg.qIndex <= 1 || cfg.qIndex >= 127 {
		return vp8Segmentation{}
	}
	sorted := append([]uint32(nil), activities...)
	slices.Sort(sorted)
	low := sorted[len(sorted)/8]
	high := sorted[len(sorted)*7/8]
	if high <= low+max(uint32(16), low/3) {
		return vp8Segmentation{}
	}

	targetCount := 2
	if cfg.maxSegments >= 3 && macroblocks >= 48 {
		targetCount = 3
	}
	if cfg.maxSegments >= 4 && macroblocks >= 192 {
		targetCount = 4
	}
	var thresholds [vp8SegmentCount - 1]uint32
	thresholdCount := 0
	for i := 1; i < targetCount; i++ {
		thresholdIndex := len(sorted)*i/targetCount - 1
		threshold := sorted[max(thresholdIndex, 0)]
		if threshold >= sorted[len(sorted)-1] {
			continue
		}
		if thresholdCount > 0 && threshold <= thresholds[thresholdCount-1] {
			continue
		}
		thresholds[thresholdCount] = threshold
		thresholdCount++
	}
	if thresholdCount == 0 {
		return vp8Segmentation{}
	}

	segmentation := vp8Segmentation{
		count:        thresholdCount + 1,
		mapIDs:       make([]uint8, macroblocks),
		activityLow:  low,
		activityHigh: high,
	}
	for macroblock, activity := range activities {
		id := uint8(0)
		for i := 0; i < thresholdCount && activity > thresholds[i]; i++ {
			id++
		}
		segmentation.mapIDs[macroblock] = id
	}
	segmentation.mapProbs = vp8SegmentMapProbabilities(segmentation.mapIDs)
	segmentation.configureSegments(cfg)
	return segmentation
}

func vp8MacroblockActivity(readLuma lumaReader, bounds image.Rectangle, mbx int, mby int) uint32 {
	const sampleDimension = 8
	var previous [sampleDimension]uint8
	var sum uint64
	var sumSquares uint64
	var gradient uint64
	for y := 0; y < sampleDimension; y++ {
		var left uint8
		for x := 0; x < sampleDimension; x++ {
			value := sampleLuma(readLuma, bounds, mbx*16+x*2, mby*16+y*2)
			sum += uint64(value)
			sumSquares += uint64(value) * uint64(value)
			if x > 0 {
				gradient += uint64(absInt(int(value) - int(left)))
			}
			if y > 0 {
				gradient += uint64(absInt(int(value) - int(previous[x])))
			}
			left = value
			previous[x] = value
		}
	}
	const samples = sampleDimension * sampleDimension
	variance := (sumSquares*uint64(samples) - sum*sum + samples*samples/2) / (samples * samples)
	meanGradient := (gradient + samples - 1) / (2*samples - sampleDimension*2)
	return uint32(min(variance+meanGradient*meanGradient*4, uint64(^uint32(0))))
}

func (s *vp8Segmentation) configureSegments(cfg vp8LossyConfig) {
	strength := clipInt((cfg.qIndex+15)/16, 1, 6)
	offsets := [vp8SegmentCount]int{}
	switch s.count {
	case 2:
		offsets = [vp8SegmentCount]int{-(strength + 1) / 2, strength}
	case 3:
		offsets = [vp8SegmentCount]int{-strength, 0, strength}
	default:
		offsets = [vp8SegmentCount]int{-2 * strength, -strength, strength, 2 * strength}
	}
	for i := 0; i < s.count; i++ {
		quant := vp8QuantForIndexDeltas(clipInt(cfg.qIndex+offsets[i], 0, 127), cfg.quantDeltas)
		s.segments[i] = vp8SegmentConfig{
			quant:       quant,
			filterLevel: vp8LoopFilterForQuant(quant).level,
			rd:          newVP8RDConfig(quant),
		}
	}
}

func vp8SegmentMapProbabilities(mapIDs []uint8) [3]uint8 {
	var counts [vp8SegmentCount]int
	for _, id := range mapIDs {
		if id < vp8SegmentCount {
			counts[id]++
		}
	}
	left := counts[0] + counts[1]
	right := counts[2] + counts[3]
	return [3]uint8{
		vp8BranchProbability(left, left+right),
		vp8BranchProbability(counts[0], left),
		vp8BranchProbability(counts[2], right),
	}
}

func vp8BranchProbability(falseCount int, total int) uint8 {
	if total <= 0 || falseCount >= total {
		return 255
	}
	if falseCount <= 0 {
		return 0
	}
	return uint8(clipInt((falseCount*255+total/2)/total, 0, 255))
}

func writeVP8SegmentationHeader(enc *vp8BoolEncoder, segmentation *vp8Segmentation) {
	if !segmentation.enabled() {
		enc.writeBitEqualProb(false)
		return
	}

	enc.writeBitEqualProb(true) // segmentation enabled
	enc.writeBitEqualProb(true) // update macroblock segment map
	enc.writeBitEqualProb(true) // update segment feature data
	enc.writeBitEqualProb(true) // absolute feature values
	for _, segment := range segmentation.segments {
		writeVP8OptionalSignedLiteral(enc, segment.quant.qIndex, 7)
	}
	for _, segment := range segmentation.segments {
		writeVP8OptionalSignedLiteral(enc, segment.filterLevel, 6)
	}
	for _, prob := range segmentation.mapProbs {
		if prob == 255 {
			enc.writeBitEqualProb(false)
			continue
		}
		enc.writeBitEqualProb(true)
		writeVP8Literal(enc, uint32(prob), 8)
	}
}

func writeVP8OptionalSignedLiteral(enc *vp8BoolEncoder, value int, bits uint8) {
	if value == 0 {
		enc.writeBitEqualProb(false)
		return
	}
	enc.writeBitEqualProb(true)
	if value < 0 {
		writeVP8Literal(enc, uint32(-value), bits)
		enc.writeBitEqualProb(true)
		return
	}
	writeVP8Literal(enc, uint32(value), bits)
	enc.writeBitEqualProb(false)
}

func writeVP8SegmentID(enc *vp8BoolEncoder, segmentation *vp8Segmentation, macroblock int) {
	id := segmentation.mapIDs[macroblock]
	if id < 2 {
		enc.writeBit(segmentation.mapProbs[0], false)
		enc.writeBit(segmentation.mapProbs[1], id == 1)
		return
	}
	enc.writeBit(segmentation.mapProbs[0], true)
	enc.writeBit(segmentation.mapProbs[2], id == 3)
}
