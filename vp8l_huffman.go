package webp

import "slices"

type vp8lTreeKind uint8

const (
	vp8lTreeSimple vp8lTreeKind = iota
	vp8lTreeNormal
	vp8lTreeFull8
)

type vp8lHuffmanTree struct {
	kind                  vp8lTreeKind
	alphabetSize          int
	headerBitLen          uint64
	symbols               [2]uint16
	symbolCount           uint8
	lengths               []uint8
	codes                 []uint16
	codeLengthTokens      []alphaCodeLengthToken
	codeLengthLengths     [alphaCodeLengthCodeCount]uint8
	codeLengthCodes       [alphaCodeLengthCodeCount]uint16
	codeLengthCodeCount   uint8
	codeLengthSymbolLimit int
}

type vp8lHuffmanWorkspace struct {
	counts          []uint32
	lengths         []uint8
	nodes           []huffmanNode
	active          []int
	symbols         []huffmanSymbol
	groupCosts      []uint8
	groupRemap      map[uint16]uint16
	compactedGroups []uint16
	denseHistograms []vp8lDenseHistogram
}

func buildVP8LHuffmanTree(counts []uint32) vp8lHuffmanTree {
	return buildVP8LHuffmanTreeWorkspace(counts, nil)
}

func buildVP8LHuffmanTreeWorkspace(counts []uint32, workspace *vp8lHuffmanWorkspace) vp8lHuffmanTree {
	tree := vp8lHuffmanTree{alphabetSize: len(counts)}
	var symbols [2]uint16
	n := 0
	highSymbol := false
	for symbol, count := range counts {
		if count == 0 {
			continue
		}
		if symbol > 255 {
			highSymbol = true
		}
		if n < len(symbols) {
			symbols[n] = uint16(symbol)
		}
		n++
	}
	if n == 0 {
		tree.kind = vp8lTreeSimple
		tree.symbolCount = 1
		return vp8lFinalizeHuffmanTree(tree)
	}
	if n <= 2 && !highSymbol {
		tree.kind = vp8lTreeSimple
		tree.symbolCount = uint8(n)
		tree.symbols = symbols
		return vp8lFinalizeHuffmanTree(tree)
	}

	var normalCounts []uint32
	var lengths []uint8
	if workspace == nil {
		normalCounts = append([]uint32(nil), counts...)
		lengths = make([]uint8, len(counts))
	} else {
		workspace.counts = vp8lResizeUint32s(workspace.counts, len(counts))
		copy(workspace.counts, counts)
		normalCounts = workspace.counts
		workspace.lengths = vp8lResizeUint8s(workspace.lengths, len(counts))
		lengths = workspace.lengths
	}
	if n == 1 {
		for symbol := range normalCounts {
			if normalCounts[symbol] == 0 {
				normalCounts[symbol] = 1
				break
			}
		}
	}
	if workspace == nil {
		if !huffmanCodeLengthsInto(lengths, normalCounts) {
			lengths = vp8lBalancedCodeLengths(normalCounts)
		}
	} else {
		workspace.nodes = vp8lResizeHuffmanNodes(workspace.nodes, maxInt(1, 2*len(counts)-1))
		workspace.active = vp8lResizeIntsCapacity(workspace.active, len(counts))
		ok := huffmanCodeLengthsIntoWorkspace(lengths, normalCounts, workspace.nodes[:0], workspace.active[:0], 15, false)
		if !ok {
			vp8lBalancedCodeLengthsIntoWorkspace(lengths, normalCounts, workspace)
		}
	}
	tree = newVP8LNormalTree(lengths)

	normalBits := tree.headerBits() + vp8lHuffmanDataBits(counts, tree.lengths)
	if !highSymbol && len(counts) >= nLiteralCodes {
		full8Bits := vp8lFull8TreeBits(len(counts)) + vp8lTotalCounts(counts)*8
		if full8Bits <= normalBits {
			return vp8lFinalizeHuffmanTree(vp8lHuffmanTree{kind: vp8lTreeFull8, alphabetSize: len(counts)})
		}
	}
	return tree
}

func vp8lResizeUint8s(values []uint8, length int) []uint8 {
	if cap(values) < length {
		return make([]uint8, length)
	}
	return values[:length]
}

func vp8lResizeHuffmanNodes(values []huffmanNode, length int) []huffmanNode {
	if cap(values) < length {
		return make([]huffmanNode, length)
	}
	return values[:length]
}

func vp8lResizeIntsCapacity(values []int, length int) []int {
	if cap(values) < length {
		return make([]int, length)
	}
	return values[:length]
}

func vp8lBalancedCodeLengthsIntoWorkspace(lengths []uint8, counts []uint32, workspace *vp8lHuffmanWorkspace) {
	clear(lengths)
	workspace.symbols = workspace.symbols[:0]
	for symbol, count := range counts {
		if count != 0 {
			workspace.symbols = append(workspace.symbols, huffmanSymbol{count: count, symbol: symbol})
		}
	}
	slices.SortFunc(workspace.symbols, func(a huffmanSymbol, b huffmanSymbol) int {
		if a.count > b.count {
			return -1
		}
		if a.count < b.count {
			return 1
		}
		return a.symbol - b.symbol
	})
	longLength := ceilLog2(len(workspace.symbols))
	shortLength := longLength - 1
	shortCount := 1<<longLength - len(workspace.symbols)
	for i, symbol := range workspace.symbols {
		length := longLength
		if i < shortCount {
			length = shortLength
		}
		lengths[symbol.symbol] = uint8(length)
	}
}

func newVP8LNormalTree(lengths []uint8) vp8lHuffmanTree {
	tokens := alphaCodeLengthTokens(lengths)
	usage := alphaCodeLengthCodeUsageForTokens(tokens)
	codeLengthLengths, nCodes := alphaCodeLengthCodeLengthsForUsage(usage)
	return vp8lFinalizeHuffmanTree(vp8lHuffmanTree{
		kind:                  vp8lTreeNormal,
		alphabetSize:          len(lengths),
		lengths:               append([]uint8(nil), lengths...),
		codes:                 vp8lCanonicalCodes(lengths),
		codeLengthTokens:      append([]alphaCodeLengthToken(nil), tokens...),
		codeLengthLengths:     codeLengthLengths,
		codeLengthCodes:       canonicalAlphaCodeLengthCodes(codeLengthLengths),
		codeLengthCodeCount:   uint8(nCodes),
		codeLengthSymbolLimit: len(tokens),
	})
}

func (t *vp8lHuffmanTree) writeHeader(bits *vp8lBitSink) {
	if bits.writer == nil && t.headerBitLen != 0 {
		bits.bitLen += t.headerBitLen
		return
	}
	t.writeHeaderUncached(bits)
}

func (t *vp8lHuffmanTree) writeHeaderUncached(bits *vp8lBitSink) {
	switch t.kind {
	case vp8lTreeSimple:
		writeVP8LSimpleTreeSymbols(bits, t.symbols, t.symbolCount)
	case vp8lTreeNormal:
		bits.writeBits(0, 1)
		bits.writeBits(uint32(t.codeLengthCodeCount-4), 4)
		for _, symbol := range normalCodeLengthCodeOrder[:t.codeLengthCodeCount] {
			bits.writeBits(uint32(t.codeLengthLengths[symbol]), 3)
		}
		writeVP8LCodeLengthLimit(bits, t.codeLengthSymbolLimit, t.alphabetSize)
		for _, token := range t.codeLengthTokens {
			length := t.codeLengthLengths[token.symbol]
			bits.writeBits(uint32(reverseBits(t.codeLengthCodes[token.symbol], length)), length)
			bits.writeBits(token.extra, token.extraBits)
		}
	case vp8lTreeFull8:
		writeVP8LFull8Tree(bits, t.alphabetSize)
	}
}

func (t *vp8lHuffmanTree) writeSymbol(bits *vp8lBitSink, symbol int) {
	if bits.writer == nil {
		switch t.kind {
		case vp8lTreeSimple:
			if t.symbolCount == 2 {
				bits.bitLen++
			}
		case vp8lTreeNormal:
			bits.bitLen += uint64(t.lengths[symbol])
		case vp8lTreeFull8:
			bits.bitLen += 8
		}
		return
	}
	switch t.kind {
	case vp8lTreeSimple:
		if t.symbolCount < 2 {
			return
		}
		if symbol == int(t.symbols[0]) {
			bits.writeBits(0, 1)
		} else {
			bits.writeBits(1, 1)
		}
	case vp8lTreeNormal:
		length := t.lengths[symbol]
		bits.writeBits(uint32(reverseBits(t.codes[symbol], length)), length)
	case vp8lTreeFull8:
		bits.writeBits(uint32(vp8lReverse8(uint8(symbol))), 8)
	}
}

func (t *vp8lHuffmanTree) headerBits() uint64 {
	if t.headerBitLen != 0 {
		return t.headerBitLen
	}
	counter := vp8lBitCounter()
	t.writeHeaderUncached(counter)
	return counter.bitLen
}

func vp8lFinalizeHuffmanTree(tree vp8lHuffmanTree) vp8lHuffmanTree {
	counter := vp8lBitCounter()
	tree.writeHeaderUncached(counter)
	tree.headerBitLen = counter.bitLen
	return tree
}

func writeVP8LSimpleTreeSymbols(bits *vp8lBitSink, symbols [2]uint16, count uint8) {
	if count == 0 {
		count = 1
	}
	bits.writeBits(1, 1)
	bits.writeBits(uint32(count-1), 1)
	first := symbols[0]
	if first < 2 {
		bits.writeBits(0, 1)
		bits.writeBits(uint32(first), 1)
	} else {
		bits.writeBits(1, 1)
		bits.writeBits(uint32(first), 8)
	}
	if count == 2 {
		bits.writeBits(uint32(symbols[1]), 8)
	}
}

func writeVP8LCodeLengthLimit(bits *vp8lBitSink, maxSymbol int, alphabetSize int) {
	if maxSymbol >= alphabetSize {
		bits.writeBits(0, 1)
		return
	}
	if maxSymbol < 2 {
		maxSymbol = 2
	}
	value := maxSymbol - 2
	nBits := uint8(2)
	selector := uint32(0)
	for value >= 1<<nBits {
		nBits += 2
		selector++
	}
	bits.writeBits(1, 1)
	bits.writeBits(selector, 3)
	bits.writeBits(uint32(value), nBits)
}

func vp8lCanonicalCodes(lengths []uint8) []uint16 {
	var histogram [16]uint16
	for _, length := range lengths {
		if length != 0 {
			histogram[length]++
		}
	}
	var next [16]uint16
	code := uint16(0)
	for length := 1; length < len(next); length++ {
		code = (code + histogram[length-1]) << 1
		next[length] = code
	}
	codes := make([]uint16, len(lengths))
	for symbol, length := range lengths {
		if length == 0 {
			continue
		}
		codes[symbol] = next[length]
		next[length]++
	}
	return codes
}

func vp8lHuffmanDataBits(counts []uint32, lengths []uint8) uint64 {
	var bits uint64
	for symbol, count := range counts {
		bits += uint64(count) * uint64(lengths[symbol])
	}
	return bits
}

func vp8lTotalCounts(counts []uint32) uint64 {
	var total uint64
	for _, count := range counts {
		total += uint64(count)
	}
	return total
}

func vp8lFull8TreeBits(alphabetSize int) uint64 {
	return 1 + 4 + uint64(len(vp8lFull8CodeLengthCodeLengths))*3 + 1 + uint64(alphabetSize)
}

func vp8lBalancedCodeLengths(counts []uint32) []uint8 {
	used := make([]int, 0, len(counts))
	for symbol, count := range counts {
		if count != 0 {
			used = append(used, symbol)
		}
	}
	lengths := make([]uint8, len(counts))
	if len(used) == 0 {
		return lengths
	}
	longLength := ceilLog2(len(used))
	if longLength < 1 {
		longLength = 1
	}
	shortLength := longLength - 1
	shortCount := 1<<longLength - len(used)
	for i, symbol := range used {
		length := longLength
		if i < shortCount {
			length = shortLength
		}
		lengths[symbol] = uint8(length)
	}
	return lengths
}
