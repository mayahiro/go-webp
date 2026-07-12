//go:build webp_lossy_counters

package webp

import (
	"image/color"
	"sync/atomic"
)

var lossyDebugCounters [lossyCounterCount]atomic.Uint64

func countLossyCounter(kind lossyCounterKind, value uint64) {
	lossyDebugCounters[kind].Add(value)
}

func setLossyCounter(kind lossyCounterKind, value uint64) {
	lossyDebugCounters[kind].Store(value)
}

func countLossySkippedMacroblocks(skipMap []bool) {
	for _, skipped := range skipMap {
		if skipped {
			countLossyCounter(lossyCounterSkippedMacroblocks, 1)
		}
	}
}

func countLossyAlphaFilters(filters [4]bool) {
	for _, enabled := range filters {
		if enabled {
			countLossyCounter(lossyCounterAlphaFilters, 1)
		}
	}
}

func countLossyAlphaSymbol(symbol int) {
	if symbol < nLiteralCodes {
		countLossyCounter(lossyCounterAlphaLiterals, 1)
	}
}

func countLossyAlphaCopy() {
	countLossyCounter(lossyCounterAlphaCopies, 1)
}

func instrumentLossyPixelReader(read pixelReader) pixelReader {
	if read == nil {
		return nil
	}
	return func(x int, y int) color.NRGBA {
		countLossyCounter(lossyCounterSourcePixelReads, 1)
		return read(x, y)
	}
}

func instrumentLossyLumaReader(read lumaReader) lumaReader {
	if read == nil {
		return nil
	}
	return func(x int, y int) uint8 {
		countLossyCounter(lossyCounterSourcePixelReads, 1)
		return read(x, y)
	}
}

func instrumentLossyChromaReader(read chromaReader) chromaReader {
	if read == nil {
		return nil
	}
	return func(x int, y int) (uint8, uint8) {
		countLossyCounter(lossyCounterSourcePixelReads, 1)
		return read(x, y)
	}
}

func resetLossyCountersForTest() {
	for i := range lossyDebugCounters {
		lossyDebugCounters[i].Store(0)
	}
}

func lossyCountersForTest() lossyCounterSnapshot {
	value := func(kind lossyCounterKind) uint64 {
		return lossyDebugCounters[kind].Load()
	}
	return lossyCounterSnapshot{
		SourcePixelReads:         value(lossyCounterSourcePixelReads),
		RGBToYUVConversions:      value(lossyCounterRGBToYUVConversions),
		YCbCrDirectConversions:   value(lossyCounterYCbCrDirectConversions),
		PreparedSourceBytes:      value(lossyCounterPreparedSourceBytes),
		ChromaFilterSamples:      value(lossyCounterChromaFilterSamples),
		SharpChromaCandidates:    value(lossyCounterSharpChromaCandidates),
		Macroblocks:              value(lossyCounterMacroblocks),
		SegmentsConsidered:       value(lossyCounterSegmentsConsidered),
		SegmentsSelected:         value(lossyCounterSegmentsSelected),
		SegmentMapBits:           value(lossyCounterSegmentMapBits),
		Y16ModesScored:           value(lossyCounterY16ModesScored),
		Y4BlocksConsidered:       value(lossyCounterY4BlocksConsidered),
		Y4ModesScored:            value(lossyCounterY4ModesScored),
		Y4MacroblocksSelected:    value(lossyCounterY4MacroblocksSelected),
		ChromaModesScored:        value(lossyCounterChromaModesScored),
		ForwardDCTCount:          value(lossyCounterForwardDCTCount),
		InverseDCTCount:          value(lossyCounterInverseDCTCount),
		RDPasses:                 value(lossyCounterRDPasses),
		ResidualCollectionPasses: value(lossyCounterResidualCollectionPasses),
		ResidualBlocks:           value(lossyCounterResidualBlocks),
		TrellisBlocks:            value(lossyCounterTrellisBlocks),
		TrellisCandidateScores:   value(lossyCounterTrellisCandidateScores),
		TrellisPasses:            value(lossyCounterTrellisPasses),
		TrellisCoefficientVisits: value(lossyCounterTrellisCoefficientVisits),
		TrellisLevelChanges:      value(lossyCounterTrellisLevelChanges),
		SkipCandidates:           value(lossyCounterSkipCandidates),
		SkippedMacroblocks:       value(lossyCounterSkippedMacroblocks),
		TokenProbUpdatesTested:   value(lossyCounterTokenProbUpdatesTested),
		TokenProbUpdatesSelected: value(lossyCounterTokenProbUpdatesSelected),
		FilterCandidates:         value(lossyCounterFilterCandidates),
		SelectedFilterLevel:      value(lossyCounterSelectedFilterLevel),
		AlphaFilters:             value(lossyCounterAlphaFilters),
		AlphaLiterals:            value(lossyCounterAlphaLiterals),
		AlphaCopies:              value(lossyCounterAlphaCopies),
		AlphaOptimalRows:         value(lossyCounterAlphaOptimalRows),
		FirstPartitionBits:       value(lossyCounterFirstPartitionBits),
		FirstPartitionFallbacks:  value(lossyCounterFirstPartitionFallbacks),
	}
}
