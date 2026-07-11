package webp

const (
	vp8lMatchGraphMinPixels = 256 * 256
	vp8lMatchGraphMaxBytes  = 32 << 20
)

type vp8lCompactMatch struct {
	distance uint32
	length   uint16
	_        uint16
}

type vp8lMatchGraph struct {
	matches         []vp8lCompactMatch
	width           int
	total           int
	candidateCounts [2]int
	candidateCount  int
}

func (w *vp8lLZ77Workspace) buildMatchGraph(pixels []uint32, width int, candidateCounts []int) (vp8lMatchGraph, bool) {
	total := len(pixels)
	if len(candidateCounts) == 0 || len(candidateCounts) > len([2]int{}) {
		return vp8lMatchGraph{}, false
	}
	maxCandidates := clipInt(candidateCounts[len(candidateCounts)-1], vp8lMinHashCandidates, vp8lMaxHashCandidates)
	entryCount := uint64(total) * uint64(len(candidateCounts))
	if total < vp8lMinBackwardRefLength*2 || entryCount > vp8lMatchGraphMaxBytes/8 {
		return vp8lMatchGraph{}, false
	}
	w.matchGraphEntries = resizeVP8LCompactMatches(w.matchGraphEntries, int(entryCount))
	clear(w.matchGraphEntries)
	w.resetHashTables(maxCandidates)
	for pos := 0; pos+vp8lMinBackwardRefLength <= total; pos++ {
		hash := vp8lOptimalHashAt(pixels, pos)
		candidates := vp8lHashCandidatesFor(w.hashTables.primary[hash], w.hashTables.extra[hash], maxCandidates)
		offset := pos * len(candidateCounts)
		best := vp8lCompactMatch{}
		countIndex := 0
		for i := 0; i < maxCandidates; i++ {
			matchPos := int(candidates[i])
			if matchPos >= 0 && matchPos < pos {
				distance := pos - matchPos
				if _, ok := vp8lDistanceCodeForPositionDistance(distance, width); ok {
					length := vp8lPackedMatchLength(pixels, matchPos, pos)
					if length >= vp8lMinBackwardRefLength && (length > int(best.length) || length == int(best.length) && distance < int(best.distance)) {
						best = vp8lCompactMatch{distance: uint32(distance), length: uint16(length)}
					}
				}
			}
			if i+1 == candidateCounts[countIndex] {
				w.matchGraphEntries[offset+countIndex] = best
				countIndex++
				if countIndex == len(candidateCounts) {
					break
				}
			}
		}
		for countIndex < len(candidateCounts) {
			w.matchGraphEntries[offset+countIndex] = best
			countIndex++
		}
		vp8lInsertOptimalHash(&w.hashTables.primary, &w.hashTables.extra, maxCandidates, pixels, pos)
	}
	var storedCounts [2]int
	copy(storedCounts[:], candidateCounts)
	return vp8lMatchGraph{
		matches:         w.matchGraphEntries,
		width:           width,
		total:           total,
		candidateCounts: storedCounts,
		candidateCount:  len(candidateCounts),
	}, true
}

func (g vp8lMatchGraph) available() bool {
	return len(g.matches) == g.total*g.candidateCount && g.total > 0
}

func (g vp8lMatchGraph) supports(candidateCount int) bool {
	for i := 0; i < g.candidateCount; i++ {
		if g.candidateCounts[i] == candidateCount {
			return true
		}
	}
	return false
}

func (g vp8lMatchGraph) best(pos int, candidateCount int) vp8lMatch {
	if !g.available() || pos < 0 || pos >= g.total {
		return vp8lMatch{}
	}
	countIndex := -1
	for i := 0; i < g.candidateCount; i++ {
		if g.candidateCounts[i] == candidateCount {
			countIndex = i
			break
		}
	}
	if countIndex < 0 {
		return vp8lMatch{}
	}
	compact := g.matches[pos*g.candidateCount+countIndex]
	if compact.length < vp8lMinBackwardRefLength {
		return vp8lMatch{}
	}
	distance := int(compact.distance)
	distanceCode, ok := vp8lDistanceCodeForPositionDistance(distance, g.width)
	if !ok {
		return vp8lMatch{}
	}
	return vp8lMatch{length: int(compact.length), distance: distance, distanceCode: distanceCode}
}

func (g vp8lMatchGraph) optimalMatches(pos int, candidateCount int, maxMatchLength int) ([vp8lMaxHashCandidates]vp8lMatch, int) {
	var matches [vp8lMaxHashCandidates]vp8lMatch
	match := g.best(pos, candidateCount)
	match.length = minInt(match.length, minInt(g.total-pos, minInt(maxMatchLength, vp8lMaxBackwardRefLength)))
	if match.length < vp8lMinBackwardRefLength {
		return matches, 0
	}
	matches[0] = match
	return matches, 1
}

func vp8lPackedMatchLength(pixels []uint32, matchPos int, pos int) int {
	maxLength := minInt(len(pixels)-pos, vp8lMaxBackwardRefLength)
	length := 0
	for length < maxLength && pixels[matchPos+length] == pixels[pos+length] {
		length++
	}
	return length
}

func resizeVP8LCompactMatches(values []vp8lCompactMatch, length int) []vp8lCompactMatch {
	if length > cap(values) {
		return make([]vp8lCompactMatch, length)
	}
	return values[:length]
}
