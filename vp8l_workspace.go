package webp

type vp8lTransformWorkspace struct {
	buffers [3][]uint32
}

type vp8lSearchWorkspace struct {
	transform vp8lTransformWorkspace
	huffman   vp8lHuffmanWorkspace
	counters  *vp8lSearchCounters

	matchHead     []int32
	matchPrevious []int32
	matchStarts   []uint32
	matchEdges    []vp8lMatch

	dpCosts    []uint64
	dpSelected []vp8lToken

	cacheValues        []uint32
	cacheValid         []bool
	cacheHits          []int32
	cacheScreenValues  []uint32
	cacheScreenValid   []bool
	cacheScreenResults []vp8lCacheScreenResult

	entropyEntryCounts []int
	entropyOffsets     []int
	entropyCursors     []int
	entropyEntries     []vp8lHistogramEntry
	entropyTiles       []vp8lSparseHistogram
	entropyDenseCounts []uint32
}

func (w *vp8lSearchWorkspace) resetEntropyDenseCounts(length int) []uint32 {
	if w == nil {
		return make([]uint32, length)
	}
	w.entropyDenseCounts = vp8lResizeUint32s(w.entropyDenseCounts, length)
	clear(w.entropyDenseCounts)
	return w.entropyDenseCounts
}

func (w *vp8lSearchWorkspace) resetEntropyHistograms(tileCount int, entryCount int) ([]int, []int, []int, []vp8lHistogramEntry, []vp8lSparseHistogram) {
	if w == nil {
		return make([]int, tileCount), make([]int, tileCount+1), make([]int, tileCount), make([]vp8lHistogramEntry, entryCount), make([]vp8lSparseHistogram, tileCount)
	}
	w.entropyEntryCounts = vp8lResizeInts(w.entropyEntryCounts, tileCount)
	w.entropyOffsets = vp8lResizeInts(w.entropyOffsets, tileCount+1)
	w.entropyCursors = vp8lResizeInts(w.entropyCursors, tileCount)
	w.entropyEntries = vp8lResizeHistogramEntries(w.entropyEntries, entryCount)
	if cap(w.entropyTiles) < tileCount {
		w.entropyTiles = make([]vp8lSparseHistogram, tileCount)
	} else {
		w.entropyTiles = w.entropyTiles[:tileCount]
		clear(w.entropyTiles)
	}
	return w.entropyEntryCounts, w.entropyOffsets, w.entropyCursors, w.entropyEntries, w.entropyTiles
}

func (w *vp8lTransformWorkspace) pixels(slot int, length int) []uint32 {
	if w == nil {
		return make([]uint32, length)
	}
	buffer := w.buffers[slot]
	if cap(buffer) < length {
		buffer = make([]uint32, length)
	} else {
		buffer = buffer[:length]
	}
	w.buffers[slot] = buffer
	return buffer
}

func vp8lAlternateTransformSlot(slot int) int {
	return slot ^ 1
}

func (w *vp8lSearchWorkspace) resetMatchGraph(total int, hashSize int) ([]int32, []int32, []uint32, []vp8lMatch) {
	if w == nil {
		return make([]int32, hashSize), make([]int32, total), make([]uint32, total), make([]vp8lMatch, 0, total)
	}
	if cap(w.matchHead) < hashSize {
		w.matchHead = make([]int32, hashSize)
	} else {
		w.matchHead = w.matchHead[:hashSize]
	}
	w.matchPrevious = vp8lResizeInt32s(w.matchPrevious, total)
	w.matchStarts = vp8lResizeUint32s(w.matchStarts, total)
	if cap(w.matchEdges) < total {
		w.matchEdges = make([]vp8lMatch, 0, total)
	} else {
		w.matchEdges = w.matchEdges[:0]
	}
	return w.matchHead, w.matchPrevious, w.matchStarts, w.matchEdges
}

func (w *vp8lSearchWorkspace) keepMatchEdges(edges []vp8lMatch) {
	if w != nil {
		w.matchEdges = edges
	}
}

func (w *vp8lSearchWorkspace) resetDP(total int) ([]uint64, []vp8lToken) {
	if w == nil {
		return make([]uint64, total+1), make([]vp8lToken, total+1)
	}
	w.dpCosts = vp8lResizeUint64s(w.dpCosts, total+1)
	w.dpSelected = vp8lResizeTokens(w.dpSelected, total+1)
	return w.dpCosts, w.dpSelected
}

func (w *vp8lSearchWorkspace) resetColorCache(pixelCount int, cacheSize int) ([]uint32, []bool, []int32) {
	if w == nil {
		return make([]uint32, cacheSize), make([]bool, cacheSize), make([]int32, pixelCount)
	}
	w.cacheValues = vp8lResizeUint32s(w.cacheValues, cacheSize)
	w.cacheValid = vp8lResizeBools(w.cacheValid, cacheSize)
	w.cacheHits = vp8lResizeInt32s(w.cacheHits, pixelCount)
	return w.cacheValues, w.cacheValid, w.cacheHits
}

func (w *vp8lSearchWorkspace) resetColorCacheScreen(totalEntries int) ([]uint32, []bool) {
	if w == nil {
		return make([]uint32, totalEntries), make([]bool, totalEntries)
	}
	w.cacheScreenValues = vp8lResizeUint32s(w.cacheScreenValues, totalEntries)
	w.cacheScreenValid = vp8lResizeBools(w.cacheScreenValid, totalEntries)
	clear(w.cacheScreenValid)
	return w.cacheScreenValues, w.cacheScreenValid
}

func vp8lResizeUint32s(values []uint32, length int) []uint32 {
	if cap(values) < length {
		return make([]uint32, length)
	}
	return values[:length]
}

func vp8lResizeUint64s(values []uint64, length int) []uint64 {
	if cap(values) < length {
		return make([]uint64, length)
	}
	return values[:length]
}

func vp8lResizeInt32s(values []int32, length int) []int32 {
	if cap(values) < length {
		return make([]int32, length)
	}
	return values[:length]
}

func vp8lResizeBools(values []bool, length int) []bool {
	if cap(values) < length {
		return make([]bool, length)
	}
	return values[:length]
}

func vp8lResizeTokens(values []vp8lToken, length int) []vp8lToken {
	if cap(values) < length {
		return make([]vp8lToken, length)
	}
	return values[:length]
}

func vp8lResizeInts(values []int, length int) []int {
	if cap(values) < length {
		return make([]int, length)
	}
	values = values[:length]
	clear(values)
	return values
}

func vp8lResizeHistogramEntries(values []vp8lHistogramEntry, length int) []vp8lHistogramEntry {
	if cap(values) < length {
		return make([]vp8lHistogramEntry, length)
	}
	return values[:length]
}
