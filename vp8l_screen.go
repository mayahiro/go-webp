package webp

import "sort"

type vp8lCandidateScore struct {
	literalBits           uint64
	localMatchPotential   uint64
	distantMatchPotential uint64
	zeroPixels            uint64
}

type vp8lLiteralCounts struct {
	green    [nLiteralCodes + nLengthCodes]uint32
	red      [nLiteralCodes]uint32
	blue     [nLiteralCodes]uint32
	alpha    [nLiteralCodes]uint32
	distance [nDistanceCodes]uint32
}

type vp8lScreenedCandidate struct {
	candidate vp8lTransformCandidate
	family    uint32
	score     vp8lCandidateScore
}

type vp8lCandidateReservoir struct {
	width      int
	height     int
	alpha      bool
	limit      int
	candidates []vp8lScreenedCandidate
}

func newVP8LCandidateReservoir(width int, height int, alpha bool, limit int) *vp8lCandidateReservoir {
	if limit < 1 {
		limit = 1
	}
	return &vp8lCandidateReservoir{width: width, height: height, alpha: alpha, limit: limit}
}

func (r *vp8lCandidateReservoir) add(candidate vp8lTransformCandidate) {
	if candidate.materialize == nil {
		pixels := candidate.pixels
		candidate.materialize = func() []uint32 { return append([]uint32(nil), pixels...) }
	}
	screened := vp8lScreenedCandidate{
		candidate: candidate,
		family:    vp8lTransformFamily(candidate.transforms),
		score:     vp8lScoreCandidate(r.width, r.height, r.alpha, candidate),
	}
	screened.candidate.pixels = nil
	familyCount := 0
	var familyIndices [2]int
	for i := range r.candidates {
		if r.candidates[i].family != screened.family {
			continue
		}
		if familyCount < len(familyIndices) {
			familyIndices[familyCount] = i
		}
		familyCount++
	}
	if familyCount >= 2 {
		first, second := vp8lSelectFamilyFrontier(r.candidates[familyIndices[0]], r.candidates[familyIndices[1]], screened)
		r.candidates[familyIndices[0]] = first
		r.candidates[familyIndices[1]] = second
		return
	}
	r.candidates = append(r.candidates, screened)
	if len(r.candidates) <= r.limit {
		return
	}
	r.removeWorstReplaceable()
}

func (r *vp8lCandidateReservoir) removeWorstReplaceable() {
	familyCounts := make(map[uint32]int, len(r.candidates))
	for _, candidate := range r.candidates {
		familyCounts[candidate.family]++
	}
	worst := -1
	for i, candidate := range r.candidates {
		if familyCounts[candidate.family] == 1 {
			continue
		}
		if worst < 0 || vp8lCandidateRank(candidate.score) > vp8lCandidateRank(r.candidates[worst].score) {
			worst = i
		}
	}
	if worst < 0 {
		for i, candidate := range r.candidates {
			if candidate.family == 0 {
				continue
			}
			if worst < 0 || vp8lCandidateRank(candidate.score) > vp8lCandidateRank(r.candidates[worst].score) {
				worst = i
			}
		}
	}
	if worst < 0 {
		worst = len(r.candidates) - 1
	}
	copy(r.candidates[worst:], r.candidates[worst+1:])
	r.candidates = r.candidates[:len(r.candidates)-1]
}

func (r *vp8lCandidateReservoir) finalists(limit int) []vp8lTransformCandidate {
	if limit < 1 {
		limit = 1
	}
	ranked := append([]vp8lScreenedCandidate(nil), r.candidates...)
	sort.SliceStable(ranked, func(i int, j int) bool {
		left := vp8lCandidateRank(ranked[i].score)
		right := vp8lCandidateRank(ranked[j].score)
		if left != right {
			return left < right
		}
		return ranked[i].family < ranked[j].family
	})
	selected := make([]vp8lTransformCandidate, 0, minInt(limit, len(ranked)))
	selectedFamilies := make(map[uint32]bool, limit)
	selectedIndices := make(map[int]bool, limit)
	appendIndex := func(index int) {
		if index < 0 || selectedIndices[index] || len(selected) == limit {
			return
		}
		candidate := ranked[index]
		selected = append(selected, candidate.candidate)
		selectedFamilies[candidate.family] = true
		selectedIndices[index] = true
	}
	appendFamily := func(family uint32) {
		for i, candidate := range ranked {
			if candidate.family != family || selectedFamilies[family] || len(selected) == limit {
				continue
			}
			appendIndex(i)
			return
		}
	}
	appendFamily(0)
	for _, candidate := range ranked {
		if vp8lFamilyContains(candidate.family, vp8lTransformColorIndex) {
			appendFamily(candidate.family)
			break
		}
	}
	appendFamily(vp8lTransformKindsFamily(vp8lTransformPredictor, vp8lTransformSubtractGreen))
	appendFamily(vp8lTransformKindsFamily(vp8lTransformSubtractGreen, vp8lTransformPredictor, vp8lTransformColor))
	appendIndex(vp8lBestCandidateIndex(ranked, vp8lCandidateLocalRank))
	appendIndex(vp8lBestCandidateIndex(ranked, vp8lCandidateDistantRank))
	for i, candidate := range ranked {
		if len(selected) == limit {
			break
		}
		if selectedFamilies[candidate.family] {
			continue
		}
		selected = append(selected, candidate.candidate)
		selectedFamilies[candidate.family] = true
		selectedIndices[i] = true
	}
	for i, candidate := range ranked {
		if len(selected) == limit {
			break
		}
		if !selectedIndices[i] {
			selected = append(selected, candidate.candidate)
			selectedIndices[i] = true
		}
	}
	return selected
}

func (r *vp8lCandidateReservoir) blockColorBases(limit int) []vp8lTransformCandidate {
	if limit < 1 {
		return nil
	}
	ranked := append([]vp8lScreenedCandidate(nil), r.candidates...)
	sort.SliceStable(ranked, func(i int, j int) bool {
		left := vp8lCandidateRank(ranked[i].score)
		right := vp8lCandidateRank(ranked[j].score)
		if left != right {
			return left < right
		}
		return ranked[i].family < ranked[j].family
	})
	selected := make([]vp8lTransformCandidate, 0, minInt(limit, len(ranked)))
	selectedIndices := make(map[int]bool, limit)
	appendFamily := func(family uint32) {
		for i, candidate := range ranked {
			if len(selected) == limit || selectedIndices[i] || candidate.family != family || !vp8lCanAddBlockColor(candidate.candidate.transforms) {
				continue
			}
			selected = append(selected, candidate.candidate)
			selectedIndices[i] = true
			return
		}
	}
	appendFamily(vp8lTransformKindsFamily(vp8lTransformSubtractGreen, vp8lTransformPredictor))
	appendFamily(0)
	appendFamily(vp8lTransformKindsFamily(vp8lTransformPredictor))
	for i, candidate := range ranked {
		if len(selected) == limit {
			break
		}
		if selectedIndices[i] || !vp8lCanAddBlockColor(candidate.candidate.transforms) {
			continue
		}
		selected = append(selected, candidate.candidate)
		selectedIndices[i] = true
	}
	return selected
}

func vp8lCanAddBlockColor(transforms []vp8lTransform) bool {
	switch len(transforms) {
	case 0:
		return true
	case 1:
		return transforms[0].kind == vp8lTransformPredictor
	case 2:
		return transforms[0].kind == vp8lTransformSubtractGreen && transforms[1].kind == vp8lTransformPredictor
	default:
		return false
	}
}

func vp8lScoreCandidate(width int, height int, alpha bool, candidate vp8lTransformCandidate) vp8lCandidateScore {
	group, dataBits := vp8lLiteralCodeGroupAndDataBits(candidate.pixels)
	plan := &vp8lPlan{
		width:      width,
		height:     height,
		alpha:      alpha,
		transforms: candidate.transforms,
	}
	counter := vp8lBitCounter()
	plan.writePrefixTo(counter)
	counter.writeBits(0, 1) // no color cache
	counter.writeBits(0, 1) // no meta-prefix image
	group.writeHeaders(counter)
	localPotential, distantPotential, zeroPixels := vp8lSampleMatchPotential(candidate.pixels, candidate.width)
	return vp8lCandidateScore{
		literalBits:           counter.bitLen + dataBits,
		localMatchPotential:   localPotential,
		distantMatchPotential: distantPotential,
		zeroPixels:            zeroPixels,
	}
}

func vp8lLiteralCodeGroupAndDataBits(pixels []uint32) (vp8lCodeGroup, uint64) {
	var counts vp8lLiteralCounts
	for _, pixel := range pixels {
		counts.observe(pixel)
	}
	return counts.codeGroupAndDataBits()
}

func (c *vp8lLiteralCounts) observe(pixel uint32) {
	c.green[uint8(pixel>>8)]++
	c.red[uint8(pixel>>16)]++
	c.blue[uint8(pixel)]++
	c.alpha[uint8(pixel>>24)]++
}

func (c *vp8lLiteralCounts) codeGroupAndDataBits() (vp8lCodeGroup, uint64) {
	group := vp8lCodeGroup{
		green:    buildVP8LHuffmanTree(c.green[:]),
		red:      buildVP8LHuffmanTree(c.red[:]),
		blue:     buildVP8LHuffmanTree(c.blue[:]),
		alpha:    buildVP8LHuffmanTree(c.alpha[:]),
		distance: buildVP8LHuffmanTree(c.distance[:]),
	}
	dataBits := vp8lTreeDataBits(c.green[:], &group.green) +
		vp8lTreeDataBits(c.red[:], &group.red) +
		vp8lTreeDataBits(c.blue[:], &group.blue) +
		vp8lTreeDataBits(c.alpha[:], &group.alpha) +
		vp8lTreeDataBits(c.distance[:], &group.distance)
	return group, dataBits
}

func vp8lTreeDataBits(counts []uint32, tree *vp8lHuffmanTree) uint64 {
	var bits uint64
	for symbol, count := range counts {
		if count != 0 {
			bits += uint64(count) * tree.symbolCost(symbol, 0)
		}
	}
	return bits
}

func vp8lCandidateRank(score vp8lCandidateScore) int64 {
	return min(vp8lCandidateLocalRank(score), vp8lCandidateDistantRank(score))
}

func vp8lCandidateLocalRank(score vp8lCandidateScore) int64 {
	return vp8lCandidateRankForPotential(score, score.localMatchPotential)
}

func vp8lCandidateDistantRank(score vp8lCandidateScore) int64 {
	return vp8lCandidateRankForPotential(score, score.distantMatchPotential)
}

func vp8lCandidateRankForPotential(score vp8lCandidateScore, potential uint64) int64 {
	benefit := potential*8 + score.zeroPixels
	if benefit >= score.literalBits {
		return 0
	}
	return int64(score.literalBits - benefit)
}

func vp8lSelectFamilyFrontier(a vp8lScreenedCandidate, b vp8lScreenedCandidate, c vp8lScreenedCandidate) (vp8lScreenedCandidate, vp8lScreenedCandidate) {
	candidates := [...]vp8lScreenedCandidate{a, b, c}
	local := 0
	for i := 1; i < len(candidates); i++ {
		if vp8lCandidateLocalRank(candidates[i].score) < vp8lCandidateLocalRank(candidates[local].score) {
			local = i
		}
	}
	distant := -1
	for i := range candidates {
		if i == local {
			continue
		}
		if distant < 0 || vp8lCandidateDistantRank(candidates[i].score) < vp8lCandidateDistantRank(candidates[distant].score) {
			distant = i
		}
	}
	return candidates[local], candidates[distant]
}

func vp8lBestCandidateIndex(candidates []vp8lScreenedCandidate, rank func(vp8lCandidateScore) int64) int {
	best := -1
	for i, candidate := range candidates {
		if best < 0 || rank(candidate.score) < rank(candidates[best].score) {
			best = i
		}
	}
	return best
}

func vp8lSampleMatchPotential(pixels []uint32, width int) (uint64, uint64, uint64) {
	if len(pixels) == 0 {
		return 0, 0, 0
	}
	step := 1
	if len(pixels) > 8192 {
		step = len(pixels) / 8192
	}
	const (
		hashBits   = 12
		hashSize   = 1 << hashBits
		matchLimit = 64
	)
	var head [hashSize]int32
	for i := range head {
		head[i] = -1
	}
	var matchedPixels uint64
	var localMatches uint64
	var zeros uint64
	coveredUntil := 0
	for position := 0; position < len(pixels); position += step {
		pixel := pixels[position]
		if pixel == 0 || pixel == 0xff000000 {
			zeros++
		}
		if position > 0 && pixels[position-1] == pixel || position >= width && pixels[position-width] == pixel {
			localMatches++
		}
		bestLength := 0
		for _, distance := range [...]int{1, width - 1, width, width + 1} {
			if distance <= 0 || distance > position || pixels[position-distance] != pixel {
				continue
			}
			bestLength = maxInt(bestLength, vp8lScreenMatchLength(pixels, position-distance, position, matchLimit))
			if bestLength == matchLimit {
				break
			}
		}
		if bestLength < matchLimit && position+2 < len(pixels) {
			hash := vp8lHashPixels(pixels, position) & (hashSize - 1)
			candidate := int(head[hash])
			if candidate >= 0 {
				bestLength = maxInt(bestLength, vp8lScreenMatchLength(pixels, candidate, position, matchLimit))
			}
			head[hash] = int32(position)
		}
		if bestLength >= vp8lMinBackwardRefLength {
			end := position + bestLength
			start := maxInt(position, coveredUntil)
			if end > start {
				matchedPixels += uint64(end - start)
				coveredUntil = end
			}
		}
	}
	return localMatches * uint64(step), matchedPixels, zeros * uint64(step)
}

func vp8lScreenMatchLength(pixels []uint32, previous int, current int, limit int) int {
	limit = minInt(limit, len(pixels)-current)
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

func vp8lTransformFamily(transforms []vp8lTransform) uint32 {
	var family uint32
	for i, transform := range transforms {
		family |= uint32(transform.kind+1) << (i * 3)
	}
	return family
}

func vp8lTransformKindsFamily(kinds ...vp8lTransformKind) uint32 {
	var family uint32
	for i, kind := range kinds {
		family |= uint32(kind+1) << (i * 3)
	}
	return family
}

func vp8lFamilyContains(family uint32, kind vp8lTransformKind) bool {
	target := uint32(kind + 1)
	for family != 0 {
		if family&7 == target {
			return true
		}
		family >>= 3
	}
	return false
}
