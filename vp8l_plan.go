package webp

import (
	"runtime"
	"sync"
)

const (
	vp8lTokenKindShift    = 62
	vp8lTokenKindMask     = uint64(3) << vp8lTokenKindShift
	vp8lCopyDistanceBits  = 21
	vp8lCopyDistanceMask  = 1<<vp8lCopyDistanceBits - 1
	vp8lCopyLengthBitMask = 1<<12 - 1
)

type vp8lTokenKind uint8

const (
	vp8lTokenLiteral vp8lTokenKind = iota
	vp8lTokenCopy
	vp8lTokenCache
)

type vp8lToken uint64

func vp8lLiteralToken(pixel uint32) vp8lToken {
	return vp8lToken(pixel)
}

func vp8lCopyToken(length int, distanceCode int) vp8lToken {
	return vp8lToken(uint64(vp8lTokenCopy)<<vp8lTokenKindShift |
		uint64(length-1)<<vp8lCopyDistanceBits |
		uint64(distanceCode))
}

func vp8lCacheToken(index int) vp8lToken {
	return vp8lToken(uint64(vp8lTokenCache)<<vp8lTokenKindShift | uint64(index))
}

func (t vp8lToken) kind() vp8lTokenKind {
	return vp8lTokenKind((uint64(t) & vp8lTokenKindMask) >> vp8lTokenKindShift)
}

func (t vp8lToken) literal() uint32 {
	return uint32(t)
}

func (t vp8lToken) copyLength() int {
	return int(uint64(t)>>vp8lCopyDistanceBits&vp8lCopyLengthBitMask) + 1
}

func (t vp8lToken) distanceCode() int {
	return int(uint64(t) & vp8lCopyDistanceMask)
}

func (t vp8lToken) cacheIndex() int {
	return int(uint64(t) & (1<<vp8lMaxColorCacheBits - 1))
}

type vp8lPlan struct {
	width       int
	height      int
	alpha       bool
	transforms  []vp8lTransform
	image       vp8lImagePlan
	payloadBits uint64
}

type vp8lImagePlan struct {
	width     int
	height    int
	cacheBits uint8
	tokens    []vp8lToken
	group     vp8lCodeGroup
	meta      *vp8lEntropyPlan
}

type vp8lEntropyPlan struct {
	prefixBits uint8
	width      int
	height     int
	groupMap   []uint16
	image      vp8lImagePlan
	groups     []vp8lCodeGroup
}

type vp8lCodeGroup struct {
	green    vp8lHuffmanTree
	red      vp8lHuffmanTree
	blue     vp8lHuffmanTree
	alpha    vp8lHuffmanTree
	distance vp8lHuffmanTree
}

func (group *vp8lCodeGroup) headerBitLen() uint64 {
	return group.green.headerBits() +
		group.red.headerBits() +
		group.blue.headerBits() +
		group.alpha.headerBits() +
		group.distance.headerBits()
}

func (p *vp8lPlan) payloadBitLen() uint64 {
	return p.payloadBits
}

type vp8lBudget struct {
	maxSourceBytes      uint64
	predictorModes      []uint8
	predictorSizeBits   []uint8
	colorSizeBits       []uint8
	colorBaseCandidates int
	colorSearchSamples  int
	trySubtractGreen    bool
	tryColor            bool
	tryPalette          bool
	tryCombined         bool
	matchChainDepth     int
	matchEdges          int
	optimalPasses       int
	tryColorCache       bool
	screenCandidates    int
	exactCandidates     int
	tryMetaPrefix       bool
	metaPrefixBits      []uint8
	maxEntropyGroups    int
	entropyIterations   int
	entropyRefinements  int
	maxWorkers          int
	maxParallelBytes    uint64
	maxWorkspaceBytes   uint64
}

func vp8lBudgetForMode(mode Mode) vp8lBudget {
	budget := vp8lBudget{
		maxSourceBytes:      vp8lMaxSourceBytes,
		predictorModes:      []uint8{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13},
		predictorSizeBits:   []uint8{4, 5, 6, 7, 8, 9},
		colorSizeBits:       []uint8{5},
		colorBaseCandidates: 0,
		colorSearchSamples:  256,
		trySubtractGreen:    true,
		tryColor:            true,
		tryPalette:          true,
		tryCombined:         true,
		matchChainDepth:     128,
		matchEdges:          4,
		optimalPasses:       1,
		tryColorCache:       true,
		screenCandidates:    24,
		exactCandidates:     6,
		tryMetaPrefix:       true,
		metaPrefixBits:      []uint8{4, 5, 6, 7},
		maxEntropyGroups:    16,
		entropyIterations:   1,
		entropyRefinements:  0,
		maxWorkers:          2,
		maxParallelBytes:    64 << 20,
		maxWorkspaceBytes:   96 << 20,
	}
	switch mode {
	case ModeFast:
		budget.predictorModes = []uint8{1, 2}
		budget.predictorSizeBits = nil
		budget.tryColor = false
		budget.colorSizeBits = nil
		budget.colorBaseCandidates = 0
		budget.tryCombined = false
		budget.matchChainDepth = 8
		budget.matchEdges = 1
		budget.optimalPasses = 0
		budget.tryColorCache = false
		budget.screenCandidates = 4
		budget.exactCandidates = 2
		budget.tryMetaPrefix = false
		budget.maxWorkers = 1
	case ModeLowMemory:
		budget.predictorModes = []uint8{1, 2, 12}
		budget.predictorSizeBits = nil
		budget.tryColor = false
		budget.colorSizeBits = nil
		budget.colorBaseCandidates = 0
		budget.tryCombined = false
		budget.matchChainDepth = 8
		budget.matchEdges = 1
		budget.optimalPasses = 0
		budget.tryColorCache = false
		budget.screenCandidates = 6
		budget.exactCandidates = 3
		budget.tryMetaPrefix = false
		budget.maxWorkers = 1
	case ModeBestCompression:
		budget.maxSourceBytes = vp8lMaxSourceBytes
		budget.predictorModes = []uint8{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13}
		budget.colorSizeBits = []uint8{4, 5, 6}
		budget.colorBaseCandidates = 2
		budget.colorSearchSamples = 1024
		budget.matchChainDepth = 128
		budget.matchEdges = 8
		budget.optimalPasses = 2
		budget.screenCandidates = 32
		budget.exactCandidates = 10
		budget.metaPrefixBits = []uint8{3, 4, 5, 6, 7, 8}
		budget.maxEntropyGroups = 16
		budget.entropyIterations = 3
		budget.entropyRefinements = 2
		budget.maxWorkers = 4
		budget.maxParallelBytes = 128 << 20
		budget.maxWorkspaceBytes = 192 << 20
	}
	return budget
}

func searchVP8L(source vp8lSource, budget vp8lBudget) (*vp8lPlan, error) {
	pixelCount := uint64(source.width) * uint64(source.height)
	if vp8lBufferedSearchBytes(pixelCount, budget) > budget.maxWorkspaceBytes {
		return nil, errVP8LSourceLimit
	}
	pixels, alpha, err := source.materialize(budget.maxSourceBytes)
	if err != nil {
		return nil, err
	}
	direct := vp8lTransformCandidate{
		width:       source.width,
		height:      source.height,
		pixels:      pixels,
		materialize: func() []uint32 { return pixels },
	}
	reservoir := newVP8LCandidateReservoir(source.width, source.height, alpha, budget.screenCandidates)
	reservoir.add(direct)
	workspace := &vp8lSearchWorkspace{}
	vp8lForEachTransformCandidateWorkspace(pixels, source.width, source.height, budget, &workspace.transform, func(candidate vp8lTransformCandidate) {
		reservoir.add(candidate)
	})
	for _, base := range reservoir.blockColorBases(budget.colorBaseCandidates) {
		basePixels := base.materialize()
		vp8lVisitBlockColorCandidates(
			basePixels,
			source.width,
			source.height,
			budget,
			base.transforms,
			base.materialize,
			&workspace.transform,
			0,
			reservoir.add,
		)
	}
	finalists := reservoir.finalists(budget.exactCandidates)
	plans := vp8lBuildFinalistPlans(source.width, source.height, alpha, finalists, budget, workspace)
	var best *vp8lPlan
	for _, plan := range plans {
		if best == nil || plan.payloadBitLen() < best.payloadBitLen() {
			best = plan
		}
	}
	return best, nil
}

func vp8lBuildFinalistPlans(width int, height int, alpha bool, finalists []vp8lTransformCandidate, budget vp8lBudget, firstWorkspace *vp8lSearchWorkspace) []*vp8lPlan {
	plans := make([]*vp8lPlan, len(finalists))
	workerCount := vp8lFinalistWorkerCount(width*height, len(finalists), budget)
	if workerCount == 1 {
		for i, candidate := range finalists {
			plans[i] = vp8lBuildCandidatePlan(width, height, alpha, candidate, budget, firstWorkspace)
		}
		return plans
	}

	workspaces := make([]*vp8lSearchWorkspace, workerCount)
	workspaces[0] = firstWorkspace
	for i := 1; i < workerCount; i++ {
		workspaces[i] = &vp8lSearchWorkspace{}
	}
	jobs := make(chan int, len(finalists))
	var wait sync.WaitGroup
	wait.Add(workerCount)
	for worker := range workerCount {
		go func(workspace *vp8lSearchWorkspace) {
			defer wait.Done()
			for index := range jobs {
				plans[index] = vp8lBuildCandidatePlan(width, height, alpha, finalists[index], budget, workspace)
			}
		}(workspaces[worker])
	}
	for index := range finalists {
		jobs <- index
	}
	close(jobs)
	wait.Wait()
	return plans
}

func vp8lBuildCandidatePlan(width int, height int, alpha bool, candidate vp8lTransformCandidate, budget vp8lBudget, workspace *vp8lSearchWorkspace) *vp8lPlan {
	pixels := candidate.pixels
	if candidate.materialize != nil {
		pixels = candidate.materialize()
	}
	return newVP8LPlanWorkspace(width, height, alpha, candidate.transforms, pixels, candidate.width, candidate.height, budget, workspace)
}

func vp8lFinalistWorkerCount(pixelCount int, finalistCount int, budget vp8lBudget) int {
	if finalistCount < 2 || pixelCount < 64*64 || budget.maxWorkers < 2 {
		return 1
	}
	workers := minInt(finalistCount, minInt(budget.maxWorkers, runtime.GOMAXPROCS(0)))
	perWorkerBytes := vp8lWorkerBytes(uint64(pixelCount), budget)
	if perWorkerBytes > budget.maxParallelBytes {
		return 1
	}
	workers = minInt(workers, int(budget.maxParallelBytes/perWorkerBytes))
	return maxInt(1, workers)
}

func vp8lBufferedSearchBytes(pixelCount uint64, budget vp8lBudget) uint64 {
	const sharedBytesPerPixel = 16 // source plane and three transform screening buffers
	return pixelCount*sharedBytesPerPixel + vp8lWorkerBytes(pixelCount, budget)
}

func vp8lWorkerBytes(pixelCount uint64, budget vp8lBudget) uint64 {
	// Candidate pixels, graph indexes, bounded edges, DP state, cache state,
	// owned tokens, and entropy scratch all scale with the transformed image.
	bytesPerPixel := uint64(44 + 8*budget.matchEdges)
	return pixelCount*bytesPerPixel + 1<<20
}

func newVP8LPlan(width int, height int, alpha bool, transforms []vp8lTransform, pixels []uint32, imageWidth int, imageHeight int, budget vp8lBudget) *vp8lPlan {
	return newVP8LPlanWorkspace(width, height, alpha, transforms, pixels, imageWidth, imageHeight, budget, nil)
}

func newVP8LPlanWorkspace(width int, height int, alpha bool, transforms []vp8lTransform, pixels []uint32, imageWidth int, imageHeight int, budget vp8lBudget, workspace *vp8lSearchWorkspace) *vp8lPlan {
	plan := &vp8lPlan{
		width:      width,
		height:     height,
		alpha:      alpha,
		transforms: append([]vp8lTransform(nil), transforms...),
		image:      buildVP8LImagePlanWorkspace(pixels, imageWidth, imageHeight, budget, workspace),
	}
	counter := vp8lBitCounter()
	plan.writeTo(counter)
	plan.payloadBits = counter.bitLen
	return plan
}

func buildVP8LLiteralImagePlan(pixels []uint32, width int, height int) vp8lImagePlan {
	tokens := make([]vp8lToken, len(pixels))
	for i, pixel := range pixels {
		tokens[i] = vp8lLiteralToken(pixel)
	}
	return vp8lImagePlan{
		width:  width,
		height: height,
		tokens: tokens,
		group:  buildVP8LCodeGroup(tokens, 0),
	}
}

func buildVP8LCodeGroup(tokens []vp8lToken, cacheBits uint8) vp8lCodeGroup {
	greenAlphabetSize := nLiteralCodes + nLengthCodes
	if cacheBits != 0 {
		greenAlphabetSize += 1 << cacheBits
	}
	greenCounts := make([]uint32, greenAlphabetSize)
	redCounts := make([]uint32, nLiteralCodes)
	blueCounts := make([]uint32, nLiteralCodes)
	alphaCounts := make([]uint32, nLiteralCodes)
	distanceCounts := make([]uint32, nDistanceCodes)
	for _, token := range tokens {
		switch token.kind() {
		case vp8lTokenLiteral:
			pixel := token.literal()
			greenCounts[uint8(pixel>>8)]++
			redCounts[uint8(pixel>>16)]++
			blueCounts[uint8(pixel)]++
			alphaCounts[uint8(pixel>>24)]++
		case vp8lTokenCopy:
			lengthPrefix := vp8lPrefixCode(token.copyLength())
			distancePrefix := vp8lDistancePrefixCode(token.distanceCode())
			greenCounts[nLiteralCodes+lengthPrefix.code]++
			distanceCounts[distancePrefix.code]++
		case vp8lTokenCache:
			greenCounts[nLiteralCodes+nLengthCodes+token.cacheIndex()]++
		}
	}
	return vp8lCodeGroup{
		green:    buildVP8LHuffmanTree(greenCounts),
		red:      buildVP8LHuffmanTree(redCounts),
		blue:     buildVP8LHuffmanTree(blueCounts),
		alpha:    buildVP8LHuffmanTree(alphaCounts),
		distance: buildVP8LHuffmanTree(distanceCounts),
	}
}
