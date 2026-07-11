package webp

type vp8lLZ77Workspace struct {
	hashTables *vp8lHashWorkspace

	greedyTokens        []vp8lToken
	greedyTokenCapacity int
	optimalTokens       [][]vp8lToken
	candidatePixels     []uint32
	packedPixels        []uint32
	matchGraphEntries   []vp8lCompactMatch
	costs               []uint64
	previous            []int32
	lengths             []uint16
	distanceCodes       []int32
	selectedCache       []int32
	colorCacheIndices   []int32
}

type vp8lHashWorkspace struct {
	primary [vp8lHashSize][vp8lMinHashCandidates]int32
	extra   [vp8lHashSize][vp8lMinHashCandidates]int32
}

func (w *vp8lLZ77Workspace) resetCandidatePixels(length int) []uint32 {
	w.candidatePixels = resizeVP8LUint32s(w.candidatePixels, length)
	return w.candidatePixels
}

func (w *vp8lLZ77Workspace) setGreedyTokenCapacity(capacity int) {
	w.greedyTokenCapacity = capacity
}

func (w *vp8lLZ77Workspace) resetHashTables(candidateCount int) {
	if w.hashTables == nil {
		w.hashTables = &vp8lHashWorkspace{}
	}
	vp8lInitHashTables(&w.hashTables.primary, &w.hashTables.extra, candidateCount)
}

func (w *vp8lLZ77Workspace) resetGreedyTokens(collect bool, capacity int) []vp8lToken {
	if !collect {
		return nil
	}
	if capacity > cap(w.greedyTokens) {
		w.greedyTokens = make([]vp8lToken, 0, capacity)
	} else {
		w.greedyTokens = w.greedyTokens[:0]
	}
	return w.greedyTokens
}

func (w *vp8lLZ77Workspace) keepGreedyTokens(tokens []vp8lToken) {
	w.greedyTokens = tokens
}

func (w *vp8lLZ77Workspace) resetOptimalTokens(pass int, capacity int) []vp8lToken {
	for len(w.optimalTokens) <= pass {
		w.optimalTokens = append(w.optimalTokens, nil)
	}
	if capacity > cap(w.optimalTokens[pass]) {
		w.optimalTokens[pass] = make([]vp8lToken, 0, capacity)
	} else {
		w.optimalTokens[pass] = w.optimalTokens[pass][:0]
	}
	return w.optimalTokens[pass]
}

func (w *vp8lLZ77Workspace) keepOptimalTokens(pass int, tokens []vp8lToken) {
	w.optimalTokens[pass] = tokens
}

func (w *vp8lLZ77Workspace) resizeOptimal(total int) ([]uint64, []int32, []uint16, []int32, []int32) {
	w.costs = resizeVP8LUint64s(w.costs, total+1)
	w.previous = resizeVP8LInt32s(w.previous, total+1)
	w.lengths = resizeVP8LUint16s(w.lengths, total+1)
	w.distanceCodes = resizeVP8LInt32s(w.distanceCodes, total+1)
	w.selectedCache = resizeVP8LInt32s(w.selectedCache, total+1)
	return w.costs, w.previous, w.lengths, w.distanceCodes, w.selectedCache
}

func (w *vp8lLZ77Workspace) resetColorCacheIndices(length int) []int32 {
	w.colorCacheIndices = resizeVP8LInt32s(w.colorCacheIndices, length)
	for i := range w.colorCacheIndices {
		w.colorCacheIndices[i] = -1
	}
	return w.colorCacheIndices
}

func resizeVP8LUint32s(values []uint32, length int) []uint32 {
	if length > cap(values) {
		return make([]uint32, length)
	}
	return values[:length]
}

func resizeVP8LUint64s(values []uint64, length int) []uint64 {
	if length > cap(values) {
		return make([]uint64, length)
	}
	return values[:length]
}

func resizeVP8LInt32s(values []int32, length int) []int32 {
	if length > cap(values) {
		return make([]int32, length)
	}
	return values[:length]
}

func resizeVP8LUint16s(values []uint16, length int) []uint16 {
	if length > cap(values) {
		return make([]uint16, length)
	}
	return values[:length]
}

func vp8lOwnLZ77PlanTokens(plan vp8lEncodingPlan) vp8lEncodingPlan {
	if plan.colorCache != nil {
		plan.lz77Tokens = nil
		return plan
	}
	if plan.lz77Tokens != nil {
		plan.lz77Tokens = append([]vp8lToken(nil), plan.lz77Tokens...)
	}
	return plan
}
