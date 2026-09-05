package webp

import "errors"

type vp8lSearchSession struct {
	source          vp8lSource
	pixels          []uint32
	alpha           bool
	palette         []uint32
	paletteOK       bool
	paletteAnalyzed bool
	scorer          *vp8lCandidateScorer
	reusableKeys    []vp8lCandidateKey
}

func newVP8LSearchSession(source vp8lSource, maxSourceBytes uint64) (*vp8lSearchSession, error) {
	pixels, alpha, err := source.materialize(maxSourceBytes)
	if err != nil {
		return nil, err
	}
	return &vp8lSearchSession{source: source, pixels: pixels, alpha: alpha}, nil
}

func searchVP8L(source vp8lSource, budget vp8lBudget) (*vp8lPlan, error) {
	pixelCount := uint64(source.width) * uint64(source.height)
	if vp8lBufferedSearchBytes(pixelCount, budget) > budget.maxWorkspaceBytes {
		return nil, errVP8LSourceLimit
	}
	session, err := newVP8LSearchSession(source, budget.maxSourceBytes)
	if err != nil {
		return nil, err
	}
	return session.search(budget, false)
}

func (s *vp8lSearchSession) search(budget vp8lBudget, reuseScoredCandidates bool) (*vp8lPlan, error) {
	pixelCount := uint64(s.source.width) * uint64(s.source.height)
	if vp8lBufferedSearchBytes(pixelCount, budget) > budget.maxWorkspaceBytes {
		return nil, errVP8LSourceLimit
	}
	budget = vp8lAdaptBudgetForPixels(budget, s.pixels)
	if vp8lBufferedSearchBytes(pixelCount, budget) > budget.maxWorkspaceBytes {
		return nil, errVP8LSourceLimit
	}
	var palette []uint32
	var paletteOK bool
	if budget.tryPalette {
		palette, paletteOK = s.paletteTable()
	}
	var earlyPalette []uint32
	if budget.earlyExitPalette && budget.tryPalette && s.source.paletted && paletteOK && len(palette) <= 16 {
		earlyPalette = palette
	}
	direct := vp8lTransformCandidate{
		width:                s.source.width,
		height:               s.source.height,
		pixels:               s.pixels,
		materialize:          func() []uint32 { return s.pixels },
		materializeWorkspace: func(*vp8lTransformWorkspace, int) []uint32 { return s.pixels },
	}
	counters := budget.counters
	reservoir := newVP8LCandidateReservoirWithScorer(s.source.width, s.source.height, s.alpha, budget.screenCandidates, counters, s.scorer)
	if reuseScoredCandidates {
		reservoir.seedScoredCandidates(s.reusableKeys)
	}
	addCandidate := func(candidate vp8lTransformCandidate) {
		counters.recordGeneratedCandidate(len(candidate.pixels), len(candidate.transforms) != 0)
		reservoir.add(candidate)
	}
	addCandidate(direct)
	workspace := &vp8lSearchWorkspace{counters: counters}
	if earlyPalette != nil {
		vp8lForEachPaletteCandidateWithTable(s.pixels, s.source.width, s.source.height, earlyPalette, budget, &workspace.transform, addCandidate)
	} else {
		generationBudget := budget
		generationBudget.tryPalette = false
		if budget.stagedCombined {
			generationBudget.tryCombined = false
		}
		vp8lForEachTransformCandidateWorkspace(s.pixels, s.source.width, s.source.height, generationBudget, &workspace.transform, addCandidate)
		if budget.tryPalette && paletteOK {
			vp8lForEachPaletteCandidateWithTable(s.pixels, s.source.width, s.source.height, palette, budget, &workspace.transform, addCandidate)
		}
		if budget.stagedCombined {
			vp8lExpandSelectedCombinedCandidates(reservoir, s.source.width, s.source.height, budget, &workspace.transform, addCandidate)
		}
		for _, base := range reservoir.blockColorBases(budget.colorBaseCandidates) {
			basePixels := vp8lMaterializeCandidateWorkspace(base, &workspace.transform, 0)
			vp8lVisitBlockColorCandidates(
				basePixels,
				s.source.width,
				s.source.height,
				budget,
				base.transforms,
				base.materialize,
				base.materializeWorkspace,
				&workspace.transform,
				1,
				addCandidate,
			)
		}
	}
	shortlist := reservoir.finalists(budget.shallowCandidates)
	finalists := vp8lSelectExactFinalists(s.source.width, s.source.height, s.alpha, shortlist, budget, workspace)
	budget.counters.recordExactFinalists(len(finalists))
	plans := vp8lBuildFinalPlans(s.source.width, s.source.height, s.alpha, finalists, budget, workspace)
	bestIndex := 0
	for i := 1; i < len(plans); i++ {
		if plans[i].payloadBitLen() < plans[bestIndex].payloadBitLen() {
			bestIndex = i
		}
	}
	if s.scorer != nil && !reuseScoredCandidates {
		s.reusableKeys = []vp8lCandidateKey{vp8lTransformCandidateKey(finalists[bestIndex])}
	}
	return plans[bestIndex], nil
}

func (s *vp8lSearchSession) paletteTable() ([]uint32, bool) {
	if !s.paletteAnalyzed {
		s.palette, s.paletteOK = vp8lPalette(s.pixels)
		s.paletteAnalyzed = true
	}
	return s.palette, s.paletteOK
}

func vp8lBestPlanOrStreaming(source vp8lSource) (vp8lEncodedPlan, error) {
	defaultBudget := vp8lBudgetForMode(ModeDefault)
	bestBudget := vp8lBudgetForMode(ModeBestCompression)
	pixelCount := uint64(source.width) * uint64(source.height)
	if vp8lBufferedSearchBytes(pixelCount, defaultBudget) > defaultBudget.maxWorkspaceBytes &&
		vp8lBufferedSearchBytes(pixelCount, bestBudget) > bestBudget.maxWorkspaceBytes {
		return vp8lBestStreamingPlan(source)
	}
	session, err := newVP8LSearchSession(source, bestBudget.maxSourceBytes)
	if errors.Is(err, errVP8LSourceLimit) {
		return vp8lBestStreamingPlan(source)
	}
	if err != nil {
		return nil, err
	}
	session.scorer = newVP8LCandidateScorer()
	defaultPlan, err := vp8lSessionPlanOrStreaming(session, source, ModeDefault, false)
	if err != nil {
		return nil, err
	}
	bestPlan, err := vp8lSessionPlanOrStreaming(session, source, ModeBestCompression, true)
	if err != nil {
		return nil, err
	}
	return vp8lSmallerPlan(defaultPlan, bestPlan), nil
}

func vp8lSessionPlanOrStreaming(session *vp8lSearchSession, source vp8lSource, mode Mode, reuseScoredCandidates bool) (vp8lEncodedPlan, error) {
	plan, err := session.search(vp8lBudgetForMode(mode), reuseScoredCandidates)
	if errors.Is(err, errVP8LSourceLimit) {
		return searchVP8LStreaming(source, mode)
	}
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func vp8lBestStreamingPlan(source vp8lSource) (vp8lEncodedPlan, error) {
	defaultPlan, err := searchVP8LStreaming(source, ModeDefault)
	if err != nil {
		return nil, err
	}
	bestPlan, err := searchVP8LStreaming(source, ModeBestCompression)
	if err != nil {
		return nil, err
	}
	return vp8lSmallerPlan(defaultPlan, bestPlan), nil
}

func vp8lSmallerPlan(left vp8lEncodedPlan, right vp8lEncodedPlan) vp8lEncodedPlan {
	if left.payloadBitLen() <= right.payloadBitLen() {
		return left
	}
	return right
}
