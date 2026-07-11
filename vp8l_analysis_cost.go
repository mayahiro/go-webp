package webp

type vp8lImageAnalysisMergeCostScratch struct {
	counts  [nLiteralCodes + nLengthCodes]uint32
	lengths [nLiteralCodes + nLengthCodes]uint8
	huffman alphaHuffmanScratch
}

func (s *vp8lImageAnalysisMergeCostScratch) mergedTreeAndDataBits(a imageAnalysis, b imageAnalysis) uint64 {
	bits := simpleTreeBits(0)
	for channel := range a.channels {
		clear(s.counts[:])
		vp8lAddChannelPlanCounts(s.counts[:nLiteralCodes], a.channels[channel])
		vp8lAddChannelPlanCounts(s.counts[:nLiteralCodes], b.channels[channel])
		alphabetSize := nLiteralCodes
		if channel == 0 {
			alphabetSize += nLengthCodes
		}
		bits += vp8lTreeAndDataBitsForCounts(s.counts[:alphabetSize], s.lengths[:alphabetSize], &s.huffman)
	}
	return bits
}

func vp8lAddChannelPlanCounts(counts []uint32, plan channelPlan) {
	if plan.histogram != nil {
		for symbol, count := range plan.histogram.counts {
			counts[symbol] += count
		}
		return
	}
	for i := 0; i < plan.n; i++ {
		counts[plan.symbols[i]] += plan.counts[i]
	}
}
