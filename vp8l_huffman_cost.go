package webp

type vp8lHuffmanCost struct {
	headerBits uint64
	dataBits   uint64
}

func buildVP8LHuffmanCostWorkspace(counts []uint32, workspace *vp8lHuffmanWorkspace) vp8lHuffmanCost {
	if workspace == nil {
		workspace = &vp8lHuffmanWorkspace{}
	}
	var symbols [2]uint16
	symbolCount := 0
	highSymbol := false
	for symbol, count := range counts {
		if count == 0 {
			continue
		}
		if symbol > 255 {
			highSymbol = true
		}
		if symbolCount < len(symbols) {
			symbols[symbolCount] = uint16(symbol)
		}
		symbolCount++
	}
	if symbolCount == 0 {
		return vp8lSimpleHuffmanCost(symbols, 1, counts)
	}
	if symbolCount <= 2 && !highSymbol {
		return vp8lSimpleHuffmanCost(symbols, uint8(symbolCount), counts)
	}

	workspace.counts = vp8lResizeUint32s(workspace.counts, len(counts))
	copy(workspace.counts, counts)
	workspace.lengths = vp8lResizeUint8s(workspace.lengths, len(counts))
	lengths := workspace.lengths
	if symbolCount == 1 {
		for symbol := range workspace.counts {
			if workspace.counts[symbol] == 0 {
				workspace.counts[symbol] = 1
				break
			}
		}
	}
	workspace.nodes = vp8lResizeHuffmanNodes(workspace.nodes, maxInt(1, 2*len(counts)-1))
	workspace.active = vp8lResizeIntsCapacity(workspace.active, len(counts))
	if !huffmanCodeLengthsIntoWorkspace(lengths, workspace.counts, workspace.nodes[:0], workspace.active[:0], 15, false) {
		vp8lBalancedCodeLengthsIntoWorkspace(lengths, workspace.counts, workspace)
	}
	normal := vp8lHuffmanCost{
		headerBits: vp8lNormalHuffmanHeaderBits(lengths),
		dataBits:   vp8lHuffmanDataBits(counts, lengths),
	}
	if !highSymbol && len(counts) >= nLiteralCodes {
		full8 := vp8lHuffmanCost{
			headerBits: vp8lFull8TreeBits(len(counts)),
			dataBits:   vp8lTotalCounts(counts) * 8,
		}
		if full8.headerBits+full8.dataBits <= normal.headerBits+normal.dataBits {
			return full8
		}
	}
	return normal
}

func vp8lSimpleHuffmanCost(symbols [2]uint16, symbolCount uint8, counts []uint32) vp8lHuffmanCost {
	counter := vp8lBitCounter()
	writeVP8LSimpleTreeSymbols(counter, symbols, symbolCount)
	var dataBits uint64
	if symbolCount == 2 {
		dataBits = vp8lTotalCounts(counts)
	}
	return vp8lHuffmanCost{headerBits: counter.bitLen, dataBits: dataBits}
}

func vp8lNormalHuffmanHeaderBits(lengths []uint8) uint64 {
	tokens := alphaCodeLengthTokens(lengths)
	usage := alphaCodeLengthCodeUsageForTokens(tokens)
	codeLengthLengths, codeCount := alphaCodeLengthCodeLengthsForUsage(usage)
	counter := vp8lBitCounter()
	counter.writeBits(0, 1)
	counter.writeBits(uint32(codeCount-4), 4)
	for _, symbol := range normalCodeLengthCodeOrder[:codeCount] {
		counter.writeBits(uint32(codeLengthLengths[symbol]), 3)
	}
	writeVP8LCodeLengthLimit(counter, len(tokens), len(lengths))
	for _, token := range tokens {
		counter.writeBits(0, codeLengthLengths[token.symbol])
		counter.writeBits(token.extra, token.extraBits)
	}
	return counter.bitLen
}
