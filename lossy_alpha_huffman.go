package webp

import (
	"image"
	"sort"
)

func writeAlphaVP8LImageStream(bits *bitWriter, readPixel pixelReader, bounds image.Rectangle, width int, height int, filter byte, plan alphaResidualPlan, code alphaCode) {
	bits.writeBits(0, 1) // no transforms
	bits.writeBits(0, 1) // no color cache
	bits.writeBits(0, 1) // no meta prefix image

	writeAlphaGreenTree(bits, code)
	writeAlphaSimpleTree(bits, 0)
	writeAlphaSimpleTree(bits, 0)
	writeAlphaSimpleTree(bits, 0)
	if code.lz77 {
		writeAlphaDistanceTree(bits, code)
	} else {
		writeAlphaSimpleTree(bits, 0)
	}

	if len(plan.tokens) != 0 {
		writeAlphaTokens(bits, code, plan.tokens)
	} else if code.lz77 && code.rowCopy {
		writeAlphaLZ77Bits(bits, readPixel, bounds, width, height, filter, code)
	} else if code.lz77 {
		writeAlphaRLEBits(bits, readPixel, bounds, width, height, filter, code)
	} else if code.n != 1 {
		writeAlphaResidualBits(bits, readPixel, bounds, width, height, filter, code)
	}
}

func writeAlphaGreenTree(bits *bitWriter, code alphaCode) {
	if code.normal {
		writeAlphaNormalTree(bits, code.lengths[:])
		return
	}
	switch code.n {
	case 1:
		writeAlphaSimpleTree(bits, uint8(code.symbols[0]))
	case 2:
		writeAlphaTwoSymbolTree(bits, uint8(code.symbols[0]), uint8(code.symbols[1]))
	default:
		writeAlphaNormalTree(bits, code.lengths[:])
	}
}

func writeAlphaDistanceTree(bits *bitWriter, code alphaCode) {
	if code.distanceNormal {
		writeAlphaNormalTree(bits, code.distanceLengths[:])
		return
	}
	switch code.distanceN {
	case 1:
		writeAlphaSimpleTree(bits, code.distanceSymbols[0])
	case 2:
		writeAlphaTwoSymbolTree(bits, code.distanceSymbols[0], code.distanceSymbols[1])
	default:
		writeAlphaSimpleTree(bits, 0)
	}
}

func writeAlphaSimpleTree(bits *bitWriter, symbol uint8) {
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

func writeAlphaTwoSymbolTree(bits *bitWriter, symbol0 uint8, symbol1 uint8) {
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

func alphaVP8LStreamSize(plan alphaResidualPlan, code alphaCode) uint64 {
	bits := uint64(3)
	bits += alphaGreenTreeBits(code)
	bits += 3 * alphaSimpleTreeBits(0)
	if code.lz77 {
		bits += alphaDistanceTreeBits(code)
	} else {
		bits += alphaSimpleTreeBits(0)
	}
	bits += alphaDataBits(plan, code)
	return (bits + 7) >> 3
}

func alphaGreenTreeBits(code alphaCode) uint64 {
	if code.normal {
		return alphaNormalTreeBits(code.lengths[:])
	}
	switch code.n {
	case 1:
		return alphaSimpleTreeBits(uint8(code.symbols[0]))
	case 2:
		return alphaTwoSymbolTreeBits(uint8(code.symbols[0]))
	default:
		return alphaNormalTreeBits(code.lengths[:])
	}
}

func alphaDistanceTreeBits(code alphaCode) uint64 {
	if code.distanceNormal {
		return alphaNormalTreeBits(code.distanceLengths[:])
	}
	switch code.distanceN {
	case 1:
		return alphaSimpleTreeBits(code.distanceSymbols[0])
	case 2:
		return alphaTwoSymbolTreeBits(code.distanceSymbols[0])
	default:
		return alphaSimpleTreeBits(0)
	}
}

func alphaSimpleTreeBits(symbol uint8) uint64 {
	if symbol < 2 {
		return 4
	}
	return 11
}

func alphaTwoSymbolTreeBits(symbol0 uint8) uint64 {
	if symbol0 < 2 {
		return 12
	}
	return 19
}

func alphaNormalTreeBits(lengths []uint8) uint64 {
	baseBits, tokenCount := alphaNormalTreeBaseBits(lengths)
	return baseBits + alphaCodeLengthLimitBits(int(tokenCount), len(lengths))
}

func alphaNormalTreeBaseBits(lengths []uint8) (uint64, uint16) {
	usage, tokenCount := alphaCodeLengthCodeUsageForLengths(lengths)
	codeLengthCodeLengths, _ := alphaCodeLengthCodeLengthsForUsage(usage)
	tokenBits := alphaCodeLengthTokenBitsWithCodeLengths(lengths, codeLengthCodeLengths)
	nCodes := alphaCodeLengthCodeCountForUsage(usage)
	return uint64(1+4+nCodes*3) + tokenBits, uint16(tokenCount)
}

func alphaCodeLengthLimitBits(maxSymbol int, alphabetSize int) uint64 {
	if maxSymbol >= alphabetSize {
		return 1
	}
	if maxSymbol < 2 {
		maxSymbol = 2
	}
	value := maxSymbol - 2
	nBits := uint8(2)
	for value >= 1<<nBits {
		nBits += 2
	}
	return uint64(1 + 3 + nBits)
}

func alphaDataBits(plan alphaResidualPlan, code alphaCode) uint64 {
	bits := alphaLiteralDataBits(plan, code)
	if code.lz77 {
		bits += alphaLengthExtraBits(plan)
		bits += alphaDistanceDataBits(plan, code)
	}
	return bits
}

func alphaLiteralDataBits(plan alphaResidualPlan, code alphaCode) uint64 {
	if code.normal {
		var bits uint64
		for symbol, count := range plan.counts {
			bits += uint64(count) * uint64(code.lengths[symbol])
		}
		return bits
	}
	switch code.n {
	case 1:
		return 0
	case 2:
		return alphaTotalSymbolCount(plan.counts[:])
	default:
		var bits uint64
		for symbol, count := range plan.counts {
			bits += uint64(count) * uint64(code.lengths[symbol])
		}
		return bits
	}
}

func alphaLengthExtraBits(plan alphaResidualPlan) uint64 {
	var bits uint64
	for code := 0; code < nLengthCodes; code++ {
		bits += uint64(plan.counts[nLiteralCodes+code]) * uint64(vp8lLengthPrefixExtraBits(code))
	}
	return bits
}

func vp8lLengthPrefixExtraBits(code int) uint8 {
	return vp8lPrefixExtraBits(code)
}

func vp8lPrefixExtraBits(code int) uint8 {
	if code < 4 {
		return 0
	}
	return uint8((code - 2) >> 1)
}

func alphaDistanceDataBits(plan alphaResidualPlan, code alphaCode) uint64 {
	var bits uint64
	if code.distanceNormal {
		for symbol, count := range plan.distanceCounts {
			bits += uint64(count) * uint64(code.distanceLengths[symbol])
		}
	} else if code.distanceN == 2 {
		bits += alphaTotalSymbolCount(plan.distanceCounts[:])
	}
	for symbol, count := range plan.distanceCounts {
		bits += uint64(count) * uint64(vp8lPrefixExtraBits(symbol))
	}
	return bits
}

func alphaTotalSymbolCount(counts []uint32) uint64 {
	var total uint64
	for _, count := range counts {
		total += uint64(count)
	}
	return total
}

func writeAlphaNormalTree(bits *bitWriter, lengths []uint8) {
	tokens := alphaCodeLengthTokens(lengths)
	usage := alphaCodeLengthCodeUsageForTokens(tokens)
	codeLengthCodeLengths, nCodes := alphaCodeLengthCodeLengthsForUsage(usage)
	codes := canonicalAlphaCodeLengthCodes(codeLengthCodeLengths)

	bits.writeBits(0, 1)
	bits.writeBits(uint32(nCodes-4), 4)
	for _, symbol := range normalCodeLengthCodeOrder[:nCodes] {
		bits.writeBits(uint32(codeLengthCodeLengths[symbol]), 3)
	}
	writeAlphaCodeLengthLimit(bits, len(tokens), len(lengths))
	for _, token := range tokens {
		length := codeLengthCodeLengths[token.symbol]
		bits.writeBits(uint32(reverseBits(codes[token.symbol], length)), length)
		bits.writeBits(token.extra, token.extraBits)
	}
}

type alphaCodeLengthToken struct {
	symbol    uint8
	extraBits uint8
	extra     uint32
}

func alphaCodeLengthCodeCountForLengths(lengths []uint8) int {
	usage, _ := alphaCodeLengthCodeUsageForLengths(lengths)
	return alphaCodeLengthCodeCountForUsage(usage)
}

func alphaCodeLengthCodeUsageForLengths(lengths []uint8) ([alphaCodeLengthCodeCount]uint32, int) {
	maxSymbol := alphaCodeLengthLimit(lengths)
	var usage [alphaCodeLengthCodeCount]uint32
	tokenCount := 0
	for i := 0; i < maxSymbol; {
		length := lengths[i]
		if length != 0 {
			run := 1
			for i+run < maxSymbol && lengths[i+run] == length {
				run++
			}
			tokenCount += countAlphaRepeatedCodeLengthRunCodeSymbols(&usage, length, run)
			i += run
			continue
		}
		run := 1
		for i+run < maxSymbol && lengths[i+run] == 0 {
			run++
		}
		tokenCount += countAlphaZeroLengthRunCodeSymbols(&usage, run)
		i += run
	}
	return usage, tokenCount
}

func alphaCodeLengthCodeCountForTokens(tokens []alphaCodeLengthToken) int {
	usage := alphaCodeLengthCodeUsageForTokens(tokens)
	return alphaCodeLengthCodeCountForUsage(usage)
}

func alphaCodeLengthCodeUsageForTokens(tokens []alphaCodeLengthToken) [alphaCodeLengthCodeCount]uint32 {
	var usage [alphaCodeLengthCodeCount]uint32
	for _, token := range tokens {
		usage[token.symbol]++
	}
	return usage
}

func alphaCodeLengthCodeCountForUsage(usage [alphaCodeLengthCodeCount]uint32) int {
	nCodes := 4
	for i, symbol := range normalCodeLengthCodeOrder {
		if usage[symbol] != 0 && i+1 > nCodes {
			nCodes = i + 1
		}
	}
	return nCodes
}

func alphaCodeLengthCodeLengthsForUsage(usage [alphaCodeLengthCodeCount]uint32) ([alphaCodeLengthCodeCount]uint8, int) {
	var lengths [alphaCodeLengthCodeCount]uint8
	var symbols [alphaCodeLengthCodeCount]huffmanSymbol
	n := 0
	for symbol, count := range usage {
		if count == 0 {
			continue
		}
		symbols[n] = huffmanSymbol{count: count, symbol: symbol}
		n++
	}
	switch n {
	case 0:
		return lengths, 4
	case 1:
		lengths[symbols[0].symbol] = 1
		return lengths, alphaCodeLengthCodeCountForUsage(usage)
	case 2:
		lengths[symbols[0].symbol] = 1
		lengths[symbols[1].symbol] = 1
		return lengths, alphaCodeLengthCodeCountForUsage(usage)
	}

	var nodes [alphaCodeLengthCodeCount*2 - 1]huffmanNode
	var active [alphaCodeLengthCodeCount]int
	if huffmanCodeLengthsIntoWorkspace(lengths[:], usage[:], nodes[:0], active[:0], alphaCodeLengthCodeMaxLength, false) {
		return lengths, alphaCodeLengthCodeCountForUsage(usage)
	}

	for i := 1; i < n; i++ {
		sym := symbols[i]
		j := i - 1
		for ; j >= 0; j-- {
			if symbols[j].count > sym.count || symbols[j].count == sym.count && symbols[j].symbol < sym.symbol {
				break
			}
			symbols[j+1] = symbols[j]
		}
		symbols[j+1] = sym
	}

	if !assignAlphaCodeLengthCodeLengths(&lengths, symbols[:n]) {
		assignBalancedAlphaCodeLengthCodeLengths(&lengths, symbols[:n])
	}
	return lengths, alphaCodeLengthCodeCountForUsage(usage)
}

func assignAlphaCodeLengthCodeLengths(lengths *[alphaCodeLengthCodeCount]uint8, symbols []huffmanSymbol) bool {
	const inf = ^uint64(0) >> 2

	var prefix [alphaCodeLengthCodeCount + 1]uint64
	for i, sym := range symbols {
		prefix[i+1] = prefix[i] + uint64(sym.count)
	}

	var dpPrev [alphaCodeLengthCodeCount + 1][alphaCodeLengthCodeKraft + 1]uint64
	var dpNext [alphaCodeLengthCodeCount + 1][alphaCodeLengthCodeKraft + 1]uint64
	var choice [alphaCodeLengthCodeMaxLength + 1][alphaCodeLengthCodeCount + 1][alphaCodeLengthCodeKraft + 1]int8
	for used := range dpPrev {
		for kraft := range dpPrev[used] {
			dpPrev[used][kraft] = inf
			dpNext[used][kraft] = inf
		}
	}
	for length := range choice {
		for used := range choice[length] {
			for kraft := range choice[length][used] {
				choice[length][used][kraft] = -1
			}
		}
	}
	dpPrev[0][0] = 0

	n := len(symbols)
	for length := 1; length <= alphaCodeLengthCodeMaxLength; length++ {
		for used := range dpNext {
			for kraft := range dpNext[used] {
				dpNext[used][kraft] = inf
			}
		}
		unit := 1 << (alphaCodeLengthCodeMaxLength - length)
		for used := 0; used <= n; used++ {
			for kraft := 0; kraft <= alphaCodeLengthCodeKraft; kraft++ {
				base := dpPrev[used][kraft]
				if base == inf {
					continue
				}
				for count := 0; used+count <= n; count++ {
					nextKraft := kraft + count*unit
					if nextKraft > alphaCodeLengthCodeKraft {
						break
					}
					cost := base + uint64(length)*(prefix[used+count]-prefix[used])
					if cost >= dpNext[used+count][nextKraft] {
						continue
					}
					dpNext[used+count][nextKraft] = cost
					choice[length][used+count][nextKraft] = int8(count)
				}
			}
		}
		dpPrev, dpNext = dpNext, dpPrev
	}
	if dpPrev[n][alphaCodeLengthCodeKraft] == inf {
		return false
	}

	used := n
	kraft := alphaCodeLengthCodeKraft
	var countsByLength [alphaCodeLengthCodeMaxLength + 1]int
	for length := alphaCodeLengthCodeMaxLength; length >= 1; length-- {
		count := int(choice[length][used][kraft])
		if count < 0 {
			return false
		}
		countsByLength[length] = count
		used -= count
		kraft -= count * (1 << (alphaCodeLengthCodeMaxLength - length))
	}
	if used != 0 || kraft != 0 {
		return false
	}

	i := 0
	for length := 1; length <= alphaCodeLengthCodeMaxLength; length++ {
		for range countsByLength[length] {
			lengths[symbols[i].symbol] = uint8(length)
			i++
		}
	}
	return i == n
}

func assignBalancedAlphaCodeLengthCodeLengths(lengths *[alphaCodeLengthCodeCount]uint8, symbols []huffmanSymbol) {
	longLength := ceilLog2(len(symbols))
	shortLength := longLength - 1
	shortCount := (1 << longLength) - len(symbols)
	for i, sym := range symbols {
		length := longLength
		if i < shortCount {
			length = shortLength
		}
		lengths[sym.symbol] = uint8(length)
	}
}

func countAlphaZeroLengthRunCodeSymbols(usage *[alphaCodeLengthCodeCount]uint32, run int) int {
	tokenCount := 0
	for run > 0 {
		switch {
		case run >= 11:
			n := run
			if n > 138 {
				n = 138
			}
			usage[alphaCodeLengthRepeatZeroBig]++
			tokenCount++
			run -= n
		case run >= 3:
			n := run
			if n > 10 {
				n = 10
			}
			usage[alphaCodeLengthRepeatZero]++
			tokenCount++
			run -= n
		default:
			usage[0]++
			tokenCount++
			run--
		}
	}
	return tokenCount
}

func countAlphaRepeatedCodeLengthRunCodeSymbols(usage *[alphaCodeLengthCodeCount]uint32, length uint8, run int) int {
	usage[length]++
	tokenCount := 1
	run--
	for run > 0 {
		if run >= 3 {
			n := alphaRepeatedCodeLengthRunChunk(run)
			usage[alphaCodeLengthRepeatPrevious]++
			tokenCount++
			run -= n
			continue
		}
		usage[length]++
		tokenCount++
		run--
	}
	return tokenCount
}

func alphaCodeLengthTokenBits(lengths []uint8) (uint64, int) {
	usage, tokenCount := alphaCodeLengthCodeUsageForLengths(lengths)
	codeLengthCodeLengths, _ := alphaCodeLengthCodeLengthsForUsage(usage)
	return alphaCodeLengthTokenBitsWithCodeLengths(lengths, codeLengthCodeLengths), tokenCount
}

func alphaCodeLengthTokenBitsWithCodeLengths(lengths []uint8, codeLengthCodeLengths [alphaCodeLengthCodeCount]uint8) uint64 {
	maxSymbol := alphaCodeLengthLimit(lengths)
	var bits uint64
	for i := 0; i < maxSymbol; {
		length := lengths[i]
		if length != 0 {
			run := 1
			for i+run < maxSymbol && lengths[i+run] == length {
				run++
			}
			bits += alphaRepeatedCodeLengthRunTokenBitsWithCodeLengths(length, run, codeLengthCodeLengths)
			i += run
			continue
		}
		run := 1
		for i+run < maxSymbol && lengths[i+run] == 0 {
			run++
		}
		bits += alphaZeroLengthRunTokenBitsWithCodeLengths(run, codeLengthCodeLengths)
		i += run
	}
	return bits
}

func alphaRepeatedCodeLengthRunTokenBitsWithCodeLengths(length uint8, run int, codeLengthCodeLengths [alphaCodeLengthCodeCount]uint8) uint64 {
	bits := uint64(codeLengthCodeLengths[length])
	run--
	for run > 0 {
		if run >= 3 {
			n := alphaRepeatedCodeLengthRunChunk(run)
			bits += uint64(codeLengthCodeLengths[alphaCodeLengthRepeatPrevious] + 2)
			run -= n
			continue
		}
		bits += uint64(codeLengthCodeLengths[length])
		run--
	}
	return bits
}

func alphaZeroLengthRunTokenBitsWithCodeLengths(run int, codeLengthCodeLengths [alphaCodeLengthCodeCount]uint8) uint64 {
	var bits uint64
	for run > 0 {
		switch {
		case run >= 11:
			n := run
			if n > 138 {
				n = 138
			}
			bits += uint64(codeLengthCodeLengths[alphaCodeLengthRepeatZeroBig] + 7)
			run -= n
		case run >= 3:
			n := run
			if n > 10 {
				n = 10
			}
			bits += uint64(codeLengthCodeLengths[alphaCodeLengthRepeatZero] + 3)
			run -= n
		default:
			bits += uint64(codeLengthCodeLengths[0])
			run--
		}
	}
	return bits
}

func alphaCodeLengthTokens(lengths []uint8) []alphaCodeLengthToken {
	maxSymbol := alphaCodeLengthLimit(lengths)
	tokens := make([]alphaCodeLengthToken, 0, maxSymbol)
	for i := 0; i < maxSymbol; {
		length := lengths[i]
		if length != 0 {
			run := 1
			for i+run < maxSymbol && lengths[i+run] == length {
				run++
			}
			tokens = appendAlphaRepeatedCodeLengthRun(tokens, length, run)
			i += run
			continue
		}
		run := 1
		for i+run < maxSymbol && lengths[i+run] == 0 {
			run++
		}
		tokens = appendAlphaZeroLengthRun(tokens, run)
		i += run
	}
	return tokens
}

func alphaCodeLengthLimit(lengths []uint8) int {
	for i := len(lengths) - 1; i >= 0; i-- {
		if lengths[i] == 0 {
			continue
		}
		if i < 1 {
			return 2
		}
		return i + 1
	}
	return 2
}

func appendAlphaRepeatedCodeLengthRun(tokens []alphaCodeLengthToken, length uint8, run int) []alphaCodeLengthToken {
	tokens = append(tokens, alphaCodeLengthToken{symbol: length})
	run--
	for run > 0 {
		if run >= 3 {
			n := alphaRepeatedCodeLengthRunChunk(run)
			tokens = append(tokens, alphaCodeLengthToken{
				symbol:    alphaCodeLengthRepeatPrevious,
				extraBits: 2,
				extra:     uint32(n - 3),
			})
			run -= n
			continue
		}
		tokens = append(tokens, alphaCodeLengthToken{symbol: length})
		run--
	}
	return tokens
}

func alphaRepeatedCodeLengthRunChunk(run int) int {
	if run <= 6 {
		return run
	}
	if run%6 == 1 {
		return 4
	}
	return 6
}

func appendAlphaZeroLengthRun(tokens []alphaCodeLengthToken, run int) []alphaCodeLengthToken {
	for run > 0 {
		switch {
		case run >= 11:
			n := run
			if n > 138 {
				n = 138
			}
			tokens = append(tokens, alphaCodeLengthToken{
				symbol:    alphaCodeLengthRepeatZeroBig,
				extraBits: 7,
				extra:     uint32(n - 11),
			})
			run -= n
		case run >= 3:
			n := run
			if n > 10 {
				n = 10
			}
			tokens = append(tokens, alphaCodeLengthToken{
				symbol:    alphaCodeLengthRepeatZero,
				extraBits: 3,
				extra:     uint32(n - 3),
			})
			run -= n
		default:
			tokens = append(tokens, alphaCodeLengthToken{symbol: 0})
			run--
		}
	}
	return tokens
}

func writeAlphaCodeLengthLimit(bits *bitWriter, maxSymbol int, alphabetSize int) {
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

func canonicalAlphaCodeLengthCodes(lengths [alphaCodeLengthCodeCount]uint8) [alphaCodeLengthCodeCount]uint16 {
	var histogram [8]uint16
	for _, length := range lengths {
		if length != 0 {
			histogram[length]++
		}
	}

	code := uint16(0)
	var nextCodes [8]uint16
	for length := 1; length < len(nextCodes); length++ {
		code = (code + histogram[length-1]) << 1
		nextCodes[length] = code
	}

	var codes [alphaCodeLengthCodeCount]uint16
	for symbol, length := range lengths {
		if length == 0 {
			continue
		}
		codes[symbol] = nextCodes[length]
		nextCodes[length]++
	}
	return codes
}

type alphaCode struct {
	n               int
	symbols         [2]uint16
	lengths         [nLiteralCodes + nLengthCodes]uint8
	codes           [nLiteralCodes + nLengthCodes]uint16
	distanceN       int
	distanceSymbols [2]uint8
	distanceLengths [nDistanceCodes]uint8
	distanceCodes   [nDistanceCodes]uint16
	lz77            bool
	rowCopy         bool
	normal          bool
	distanceNormal  bool
}

func alphaCodeFor(plan alphaResidualPlan) (alphaCode, bool) {
	var scratch alphaHuffmanScratch
	return alphaCodeForWithScratch(plan, &scratch)
}

func alphaCodeForWithScratch(plan alphaResidualPlan, scratch *alphaHuffmanScratch) (alphaCode, bool) {
	switch plan.n {
	case 0:
		return alphaCode{}, false
	case 1:
		if plan.symbols[0] < nLiteralCodes {
			return alphaCode{n: 1, symbols: plan.symbols}, true
		}
	case 2:
		if plan.symbols[0] >= nLiteralCodes || plan.symbols[1] >= nLiteralCodes {
			break
		}
		if plan.symbols[1] < 2 && plan.symbols[0] >= 2 {
			plan.symbols[0], plan.symbols[1] = plan.symbols[1], plan.symbols[0]
		}
		return alphaCode{n: 2, symbols: plan.symbols}, true
	}
	lengths, ok := huffmanCodeLengthsWithScratch(plan.counts, scratch)
	if !ok {
		return alphaCode{}, false
	}
	codes := canonicalCodes(lengths)
	distanceN, distanceSymbols, distanceLengths, distanceCodes, distanceNormal, ok := alphaDistanceCodeForWithScratch(plan, scratch)
	if !ok {
		return alphaCode{}, false
	}
	return alphaCode{
		n:               plan.n,
		lengths:         lengths,
		codes:           codes,
		distanceN:       distanceN,
		distanceSymbols: distanceSymbols,
		distanceLengths: distanceLengths,
		distanceCodes:   distanceCodes,
		normal:          true,
		distanceNormal:  distanceNormal,
	}, true
}

func alphaDistanceCodeFor(plan alphaResidualPlan) (int, [2]uint8, [nDistanceCodes]uint8, [nDistanceCodes]uint16, bool, bool) {
	var scratch alphaHuffmanScratch
	return alphaDistanceCodeForWithScratch(plan, &scratch)
}

func alphaDistanceCodeForWithScratch(plan alphaResidualPlan, scratch *alphaHuffmanScratch) (int, [2]uint8, [nDistanceCodes]uint8, [nDistanceCodes]uint16, bool, bool) {
	if plan.distanceN <= 2 {
		return plan.distanceN, plan.distanceSymbols, [nDistanceCodes]uint8{}, [nDistanceCodes]uint16{}, false, true
	}
	lengths, ok := huffmanDistanceCodeLengthsWithScratch(plan.distanceCounts, scratch)
	if !ok {
		return 0, [2]uint8{}, [nDistanceCodes]uint8{}, [nDistanceCodes]uint16{}, false, false
	}
	return plan.distanceN, plan.distanceSymbols, lengths, canonicalDistanceCodes(lengths), true, true
}

type huffmanNode struct {
	freq   uint64
	symbol int
	left   int
	right  int
}

const (
	maxAlphaHuffmanSymbols = nLiteralCodes + nLengthCodes + 64
	maxAlphaHuffmanNodes   = maxAlphaHuffmanSymbols*2 - 1
)

type huffmanSymbol struct {
	count  uint32
	symbol int
}

type alphaHuffmanScratch struct {
	nodes  [maxAlphaHuffmanNodes]huffmanNode
	active [maxAlphaHuffmanSymbols]int
}

func huffmanCodeLengths(counts [nLiteralCodes + nLengthCodes]uint32) ([nLiteralCodes + nLengthCodes]uint8, bool) {
	var lengths [nLiteralCodes + nLengthCodes]uint8
	return lengths, huffmanCodeLengthsInto(lengths[:], counts[:])
}

func huffmanCodeLengthsWithScratch(counts [nLiteralCodes + nLengthCodes]uint32, scratch *alphaHuffmanScratch) ([nLiteralCodes + nLengthCodes]uint8, bool) {
	var lengths [nLiteralCodes + nLengthCodes]uint8
	return lengths, huffmanCodeLengthsIntoScratch(lengths[:], counts[:], scratch)
}

func huffmanDistanceCodeLengths(counts [nDistanceCodes]uint32) ([nDistanceCodes]uint8, bool) {
	var lengths [nDistanceCodes]uint8
	return lengths, huffmanCodeLengthsInto(lengths[:], counts[:])
}

func huffmanDistanceCodeLengthsWithScratch(counts [nDistanceCodes]uint32, scratch *alphaHuffmanScratch) ([nDistanceCodes]uint8, bool) {
	var lengths [nDistanceCodes]uint8
	return lengths, huffmanCodeLengthsIntoScratch(lengths[:], counts[:], scratch)
}

func huffmanCodeLengthsInto(lengths []uint8, counts []uint32) bool {
	if len(counts) > maxAlphaHuffmanSymbols {
		if len(lengths) < len(counts) {
			return false
		}
		symbolCount := 0
		for _, count := range counts {
			if count != 0 {
				symbolCount++
			}
		}
		if symbolCount == 0 {
			return false
		}
		nodes := make([]huffmanNode, 0, symbolCount*2-1)
		active := make([]int, 0, symbolCount)
		return huffmanCodeLengthsIntoWorkspace(lengths, counts, nodes, active, 15, true)
	}
	var scratch alphaHuffmanScratch
	return huffmanCodeLengthsIntoScratch(lengths, counts, &scratch)
}

func huffmanCodeLengthsIntoScratch(lengths []uint8, counts []uint32, scratch *alphaHuffmanScratch) bool {
	if len(counts) > maxAlphaHuffmanSymbols || len(lengths) < len(counts) {
		return false
	}
	return huffmanCodeLengthsIntoWorkspace(lengths, counts, scratch.nodes[:0], scratch.active[:0], 15, true)
}

func huffmanCodeLengthsIntoWorkspace(lengths []uint8, counts []uint32, nodes []huffmanNode, active []int, maxLength uint8, balancedFallback bool) bool {
	clear(lengths[:len(counts)])
	for symbol, count := range counts {
		if count == 0 {
			continue
		}
		nodes = append(nodes, huffmanNode{freq: uint64(count), symbol: symbol, left: -1, right: -1})
		active = append(active, len(nodes)-1)
	}
	if len(active) == 0 {
		return false
	}
	if len(active) == 1 {
		lengths[nodes[active[0]].symbol] = 1
		return true
	}
	if len(active) == 2 {
		lengths[nodes[active[0]].symbol] = 1
		lengths[nodes[active[1]].symbol] = 1
		return true
	}

	huffmanHeapInit(active, nodes)
	for len(active) > 1 {
		var a, b int
		a, active = huffmanHeapPop(active, nodes)
		b, active = huffmanHeapPop(active, nodes)
		nodes = append(nodes, huffmanNode{
			freq:   nodes[a].freq + nodes[b].freq,
			symbol: minInt(nodes[a].symbol, nodes[b].symbol),
			left:   a,
			right:  b,
		})
		active = huffmanHeapPush(active, len(nodes)-1, nodes)
	}

	if !assignHuffmanLengths(lengths[:], nodes, active[0], 0, maxLength) {
		if !balancedFallback {
			return false
		}
		return balancedHuffmanCodeLengthsInto(lengths, counts)
	}
	return true
}

func huffmanHeapInit(indices []int, nodes []huffmanNode) {
	for i := len(indices)/2 - 1; i >= 0; i-- {
		huffmanHeapDown(indices, i, nodes)
	}
}

func huffmanHeapPop(indices []int, nodes []huffmanNode) (int, []int) {
	root := indices[0]
	last := len(indices) - 1
	indices[0] = indices[last]
	indices = indices[:last]
	if len(indices) != 0 {
		huffmanHeapDown(indices, 0, nodes)
	}
	return root, indices
}

func huffmanHeapPush(indices []int, index int, nodes []huffmanNode) []int {
	indices = append(indices, index)
	for child := len(indices) - 1; child > 0; {
		parent := (child - 1) / 2
		if !lessHuffmanNode(nodes[indices[child]], nodes[indices[parent]]) {
			break
		}
		indices[parent], indices[child] = indices[child], indices[parent]
		child = parent
	}
	return indices
}

func huffmanHeapDown(indices []int, parent int, nodes []huffmanNode) {
	for {
		left := parent*2 + 1
		if left >= len(indices) {
			return
		}
		child := left
		right := left + 1
		if right < len(indices) && lessHuffmanNode(nodes[indices[right]], nodes[indices[left]]) {
			child = right
		}
		if !lessHuffmanNode(nodes[indices[child]], nodes[indices[parent]]) {
			return
		}
		indices[parent], indices[child] = indices[child], indices[parent]
		parent = child
	}
}

func balancedHuffmanCodeLengths(counts [nLiteralCodes + nLengthCodes]uint32) ([nLiteralCodes + nLengthCodes]uint8, bool) {
	var lengths [nLiteralCodes + nLengthCodes]uint8
	return lengths, balancedHuffmanCodeLengthsInto(lengths[:], counts[:])
}

func balancedHuffmanCodeLengthsInto(lengths []uint8, counts []uint32) bool {
	var symbols []huffmanSymbol
	for symbol, count := range counts {
		if count == 0 {
			continue
		}
		symbols = append(symbols, huffmanSymbol{count: count, symbol: symbol})
	}
	switch len(symbols) {
	case 0:
		return false
	case 1:
		lengths[symbols[0].symbol] = 1
		return true
	case 2:
		lengths[symbols[0].symbol] = 1
		lengths[symbols[1].symbol] = 1
		return true
	}

	sort.Slice(symbols, func(i int, j int) bool {
		if symbols[i].count != symbols[j].count {
			return symbols[i].count > symbols[j].count
		}
		return symbols[i].symbol < symbols[j].symbol
	})

	longLength := ceilLog2(len(symbols))
	shortLength := longLength - 1
	shortCount := (1 << longLength) - len(symbols)
	for i, sym := range symbols {
		length := longLength
		if i < shortCount {
			length = shortLength
		}
		lengths[sym.symbol] = uint8(length)
	}
	return true
}

func ceilLog2(n int) int {
	length := 0
	value := 1
	for value < n {
		value <<= 1
		length++
	}
	return length
}

func lessHuffmanNode(a huffmanNode, b huffmanNode) bool {
	if a.freq != b.freq {
		return a.freq < b.freq
	}
	return a.symbol < b.symbol
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func assignHuffmanLengths(lengths []uint8, nodes []huffmanNode, index int, depth uint8, maxLength uint8) bool {
	node := nodes[index]
	if node.left < 0 && node.right < 0 {
		if depth == 0 || depth > maxLength {
			return false
		}
		lengths[node.symbol] = depth
		return true
	}
	nextDepth := depth + 1
	if nextDepth > maxLength {
		return false
	}
	return assignHuffmanLengths(lengths, nodes, node.left, nextDepth, maxLength) &&
		assignHuffmanLengths(lengths, nodes, node.right, nextDepth, maxLength)
}

func canonicalCodes(lengths [nLiteralCodes + nLengthCodes]uint8) [nLiteralCodes + nLengthCodes]uint16 {
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

	var codes [nLiteralCodes + nLengthCodes]uint16
	for symbol, length := range lengths {
		if length == 0 {
			continue
		}
		codes[symbol] = nextCodes[length]
		nextCodes[length]++
	}
	return codes
}

func canonicalDistanceCodes(lengths [nDistanceCodes]uint8) [nDistanceCodes]uint16 {
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

	var codes [nDistanceCodes]uint16
	for symbol, length := range lengths {
		if length == 0 {
			continue
		}
		codes[symbol] = nextCodes[length]
		nextCodes[length]++
	}
	return codes
}

func reverseBits(v uint16, n uint8) uint16 {
	var r uint16
	for i := uint8(0); i < n; i++ {
		r = r<<1 | v&1
		v >>= 1
	}
	return r
}

var normalCodeLengthCodeOrder = [...]uint8{
	17, 18, 0, 1, 2, 3, 4, 5, 16, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
}
