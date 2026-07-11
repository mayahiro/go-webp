package webp

type vp8lCacheHistogram struct {
	green     []uint32
	red       [nLiteralCodes]uint32
	blue      [nLiteralCodes]uint32
	alpha     [nLiteralCodes]uint32
	distance  [nDistanceCodes]uint32
	extraBits uint64
}

type vp8lCacheScreenResult struct {
	cacheBits   uint8
	hitCount    int
	cachedBits  uint64
	literalBits uint64
	cached      vp8lCacheHistogram
	literal     vp8lCacheHistogram
}

func vp8lAnalyzeColorCacheCandidates(pixels []uint32, tokens []vp8lToken, cacheBits []uint8, workspace *vp8lSearchWorkspace) []vp8lCacheScreenResult {
	results := vp8lResetCacheScreenResults(cacheBits, workspace)
	var offsets [vp8lMaxColorCacheBits]int
	totalEntries := 0
	for i, bits := range cacheBits {
		offsets[i] = totalEntries
		totalEntries += 1 << bits
	}
	values, valid := workspace.resetColorCacheScreen(totalEntries)
	if workspace != nil {
		workspace.counters.recordCacheFullScan()
	}

	tokenIndex := 0
	nextTokenPosition := 0
	var token vp8lToken
	for position, pixel := range pixels {
		atTokenStart := position == nextTokenPosition && tokenIndex < len(tokens)
		if atTokenStart {
			token = tokens[tokenIndex]
			nextTokenPosition += vp8lTokenPixelLength(token)
			tokenIndex++
			if token.kind() == vp8lTokenCopy {
				for i := range results {
					results[i].cached.observeCopy(token)
				}
			}
		}

		hash := uint32(0x1e35a7bd) * pixel
		for i := range results {
			bits := results[i].cacheBits
			cacheIndex := int(hash >> (32 - bits))
			entry := offsets[i] + cacheIndex
			hit := valid[entry] && values[entry] == pixel
			if hit {
				results[i].hitCount++
			}
			results[i].literal.observePixel(pixel, hit, cacheIndex)
			if atTokenStart && token.kind() != vp8lTokenCopy {
				results[i].cached.observePixel(pixel, hit, cacheIndex)
			}
			values[entry] = pixel
			valid[entry] = true
		}
	}

	var huffmanWorkspace *vp8lHuffmanWorkspace
	var counters *vp8lSearchCounters
	if workspace != nil {
		huffmanWorkspace = &workspace.huffman
		counters = workspace.counters
	}
	for i := range results {
		if results[i].hitCount < 8 {
			continue
		}
		results[i].cachedBits = results[i].cached.costBits(huffmanWorkspace)
		results[i].literalBits = results[i].literal.costBits(huffmanWorkspace)
		counters.recordHuffmanCostBuilds(10)
	}
	return results
}

func vp8lResetCacheScreenResults(cacheBits []uint8, workspace *vp8lSearchWorkspace) []vp8lCacheScreenResult {
	var results []vp8lCacheScreenResult
	if workspace == nil || cap(workspace.cacheScreenResults) < len(cacheBits) {
		results = make([]vp8lCacheScreenResult, len(cacheBits))
	} else {
		results = workspace.cacheScreenResults[:len(cacheBits)]
	}
	for i, bits := range cacheBits {
		greenSize := nLiteralCodes + nLengthCodes + 1<<bits
		cachedGreen := vp8lResizeUint32s(results[i].cached.green, greenSize)
		literalGreen := vp8lResizeUint32s(results[i].literal.green, greenSize)
		clear(cachedGreen)
		clear(literalGreen)
		results[i] = vp8lCacheScreenResult{
			cacheBits: bits,
			cached:    vp8lCacheHistogram{green: cachedGreen},
			literal:   vp8lCacheHistogram{green: literalGreen},
		}
	}
	if workspace != nil {
		workspace.cacheScreenResults = results
	}
	return results
}

func (h *vp8lCacheHistogram) observePixel(pixel uint32, hit bool, cacheIndex int) {
	if hit {
		h.green[nLiteralCodes+nLengthCodes+cacheIndex]++
		return
	}
	h.green[uint8(pixel>>8)]++
	h.red[uint8(pixel>>16)]++
	h.blue[uint8(pixel)]++
	h.alpha[uint8(pixel>>24)]++
}

func (h *vp8lCacheHistogram) observeCopy(token vp8lToken) {
	lengthPrefix := vp8lPrefixCode(token.copyLength())
	distancePrefix := vp8lDistancePrefixCode(token.distanceCode())
	h.green[nLiteralCodes+lengthPrefix.code]++
	h.distance[distancePrefix.code]++
	h.extraBits += uint64(lengthPrefix.extraBits + distancePrefix.extraBits)
}

func (h *vp8lCacheHistogram) costBits(workspace *vp8lHuffmanWorkspace) uint64 {
	green := buildVP8LHuffmanCostWorkspace(h.green, workspace)
	red := buildVP8LHuffmanCostWorkspace(h.red[:], workspace)
	blue := buildVP8LHuffmanCostWorkspace(h.blue[:], workspace)
	alpha := buildVP8LHuffmanCostWorkspace(h.alpha[:], workspace)
	distance := buildVP8LHuffmanCostWorkspace(h.distance[:], workspace)
	return 6 + h.extraBits +
		green.headerBits + green.dataBits +
		red.headerBits + red.dataBits +
		blue.headerBits + blue.dataBits +
		alpha.headerBits + alpha.dataBits +
		distance.headerBits + distance.dataBits
}
