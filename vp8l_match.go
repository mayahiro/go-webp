package webp

const (
	vp8lHashBits = 16
	vp8lHashSize = 1 << vp8lHashBits
)

type vp8lMatch struct {
	distanceCode      uint32
	length            uint16
	distanceSymbol    uint8
	distanceExtraBits uint8
}

type vp8lMatchGraph struct {
	starts []uint32
	edges  []vp8lMatch
}

type vp8lPositionMatch struct {
	match    vp8lMatch
	distance int
}

type vp8lSpecialMatchState struct {
	distance int
	next     uint16
}

func buildVP8LMatchGraph(pixels []uint32, width int, budget vp8lBudget) vp8lMatchGraph {
	return buildVP8LMatchGraphWorkspace(pixels, width, budget, nil)
}

func buildVP8LMatchGraphWorkspace(pixels []uint32, width int, budget vp8lBudget, workspace *vp8lSearchWorkspace) vp8lMatchGraph {
	head, previous, starts, edges := workspace.resetMatchGraph(len(pixels))
	graph := vp8lMatchGraph{starts: starts, edges: edges}
	if len(pixels) < vp8lMinBackwardRefLength || budget.matchEdges == 0 || budget.matchChainDepth == 0 {
		return graph
	}

	for i := range head {
		head[i] = -1
	}
	for i := range previous {
		previous[i] = -1
	}
	for position := 0; position+2 < len(pixels); position++ {
		hash := vp8lHashPixels(pixels, position)
		previous[position] = head[hash]
		head[hash] = int32(position)
	}

	states := vp8lNewSpecialMatchStates(width)
	matches := make([]vp8lMatch, 0, budget.matchEdges)
	for position := len(pixels) - 1; position >= 0; position-- {
		matches = matches[:0]
		matches, best := vp8lSpecialMatchesAt(pixels, width, position, states, matches, budget.matchEdges)
		matches, best = vp8lSearchHashChainAt(pixels, width, position, previous, states, matches, best, budget)
		graph.starts[position] = uint32(len(graph.edges))
		graph.edges = append(graph.edges, matches...)

		for position > 0 && vp8lCanExtendMatchLeft(pixels, position, best) {
			position--
			matches = matches[:0]
			matches, localBest := vp8lSpecialMatchesAt(pixels, width, position, states, matches, budget.matchEdges)
			best.match.length = uint16(minInt(vp8lMaxBackwardRefLength, int(best.match.length)+1))
			matches = vp8lInsertMatch(matches, best.match, budget.matchEdges)
			if vp8lBetterPositionMatch(localBest, best) {
				best = localBest
			}
			graph.starts[position] = uint32(len(graph.edges))
			graph.edges = append(graph.edges, matches...)
		}
	}
	workspace.keepMatchEdges(graph.edges)
	return graph
}

func (g vp8lMatchGraph) at(position int) []vp8lMatch {
	start := int(g.starts[position])
	end := len(g.edges)
	if position > 0 {
		end = int(g.starts[position-1])
	}
	return g.edges[start:end]
}

func vp8lNewSpecialMatchStates(width int) []vp8lSpecialMatchState {
	states := make([]vp8lSpecialMatchState, 0, 4)
	for _, distance := range [...]int{1, width - 1, width, width + 1} {
		if distance <= 0 {
			continue
		}
		duplicate := false
		for _, state := range states {
			if state.distance == distance {
				duplicate = true
				break
			}
		}
		if !duplicate {
			states = append(states, vp8lSpecialMatchState{distance: distance})
		}
	}
	return states
}

func vp8lSpecialMatchesAt(pixels []uint32, width int, position int, states []vp8lSpecialMatchState, matches []vp8lMatch, limit int) ([]vp8lMatch, vp8lPositionMatch) {
	var best vp8lPositionMatch
	maxLength := minInt(vp8lMaxBackwardRefLength, len(pixels)-position)
	for i := range states {
		state := &states[i]
		length := 0
		if state.distance <= position && pixels[position-state.distance] == pixels[position] {
			length = minInt(maxLength, int(state.next)+1)
		}
		state.next = uint16(length)
		if length < vp8lMinBackwardRefLength {
			continue
		}
		distanceCode, ok := vp8lDistanceCodeForPositionDistance(state.distance, width)
		candidate := vp8lPositionMatch{
			match:    vp8lNewMatch(length, distanceCode),
			distance: state.distance,
		}
		if !ok {
			continue
		}
		matches = vp8lInsertMatch(matches, candidate.match, limit)
		if vp8lBetterPositionMatch(candidate, best) {
			best = candidate
		}
	}
	return matches, best
}

func vp8lSearchHashChainAt(pixels []uint32, width int, position int, previous []int32, states []vp8lSpecialMatchState, matches []vp8lMatch, best vp8lPositionMatch, budget vp8lBudget) ([]vp8lMatch, vp8lPositionMatch) {
	if position+2 >= len(pixels) {
		return matches, best
	}
	maxLength := minInt(vp8lMaxBackwardRefLength, len(pixels)-position)
	if int(best.match.length) == maxLength {
		return matches, best
	}
	candidate := int(previous[position])
	for iteration := 0; candidate >= 0 && iteration < budget.matchChainDepth; iteration++ {
		distance := position - candidate
		if distance > vp8lMaxDistanceCode-120 {
			break
		}
		if vp8lIsSpecialMatchDistance(distance, states) {
			candidate = int(previous[candidate])
			continue
		}
		bestLength := int(best.match.length)
		if bestLength != 0 && pixels[candidate+bestLength] != pixels[position+bestLength] {
			candidate = int(previous[candidate])
			continue
		}
		length := vp8lMatchLength(pixels, candidate, position)
		if length >= vp8lMinBackwardRefLength {
			distanceCode, ok := vp8lDistanceCodeForPositionDistance(distance, width)
			if ok {
				match := vp8lNewMatch(length, distanceCode)
				matches = vp8lInsertMatch(matches, match, budget.matchEdges)
				positionMatch := vp8lPositionMatch{match: match, distance: distance}
				if vp8lBetterPositionMatch(positionMatch, best) {
					best = positionMatch
					if length == maxLength {
						break
					}
				}
			}
		}
		candidate = int(previous[candidate])
	}
	return matches, best
}

func vp8lCanExtendMatchLeft(pixels []uint32, position int, match vp8lPositionMatch) bool {
	return match.distance > 0 &&
		position > match.distance &&
		pixels[position-1-match.distance] == pixels[position-1]
}

func vp8lIsSpecialMatchDistance(distance int, states []vp8lSpecialMatchState) bool {
	for _, state := range states {
		if state.distance == distance {
			return true
		}
	}
	return false
}

func vp8lInsertMatch(matches []vp8lMatch, candidate vp8lMatch, limit int) []vp8lMatch {
	if limit <= 0 {
		return matches[:0]
	}
	for i, match := range matches {
		if match.distanceCode != candidate.distanceCode {
			continue
		}
		if match.length >= candidate.length {
			return matches
		}
		copy(matches[i:], matches[i+1:])
		matches = matches[:len(matches)-1]
		break
	}
	insertAt := len(matches)
	for i, match := range matches {
		if vp8lBetterMatch(candidate, match) {
			insertAt = i
			break
		}
	}
	if insertAt >= limit {
		return matches
	}
	if len(matches) < limit {
		matches = append(matches, vp8lMatch{})
	}
	copy(matches[insertAt+1:], matches[insertAt:len(matches)-1])
	matches[insertAt] = candidate
	return matches
}

func vp8lBetterMatch(left vp8lMatch, right vp8lMatch) bool {
	leftScore := vp8lMatchScore(left)
	rightScore := vp8lMatchScore(right)
	if leftScore != rightScore {
		return leftScore > rightScore
	}
	if left.length != right.length {
		return left.length > right.length
	}
	return left.distanceCode < right.distanceCode
}

func vp8lBetterPositionMatch(left vp8lPositionMatch, right vp8lPositionMatch) bool {
	if left.match.length != right.match.length {
		return left.match.length > right.match.length
	}
	return left.match.length != 0 && vp8lBetterMatch(left.match, right.match)
}

func vp8lNewMatch(length int, distanceCode int) vp8lMatch {
	distancePrefix := vp8lDistancePrefixCode(distanceCode)
	return vp8lMatch{
		distanceCode:      uint32(distanceCode),
		length:            uint16(length),
		distanceSymbol:    uint8(distancePrefix.code),
		distanceExtraBits: distancePrefix.extraBits,
	}
}

func vp8lMatchScore(match vp8lMatch) int {
	lengthPrefix := vp8lLengthPrefixCosts[match.length]
	return int(match.length)*8 - int(lengthPrefix.extraBits) - int(match.distanceExtraBits)
}

func vp8lMatchLength(pixels []uint32, previous int, current int) int {
	limit := minInt(vp8lMaxBackwardRefLength, len(pixels)-current)
	length := 0
	for length+4 <= limit &&
		pixels[previous+length] == pixels[current+length] &&
		pixels[previous+length+1] == pixels[current+length+1] &&
		pixels[previous+length+2] == pixels[current+length+2] &&
		pixels[previous+length+3] == pixels[current+length+3] {
		length += 4
	}
	for length < limit && pixels[previous+length] == pixels[current+length] {
		length++
	}
	return length
}

func vp8lHashPixels(pixels []uint32, position int) int {
	a := pixels[position]
	b := pixels[position+1]
	c := pixels[position+2]
	hash := a*0x1e35a7bd ^ b*0x85ebca6b ^ c*0xc2b2ae35
	hash ^= hash >> 16
	return int(hash >> (32 - vp8lHashBits))
}
