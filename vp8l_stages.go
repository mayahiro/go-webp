package webp

import "sort"

func vp8lBuildFinalPlans(width int, height int, alpha bool, finalists []vp8lTransformCandidate, budget vp8lBudget, workspace *vp8lSearchWorkspace) []*vp8lPlan {
	if !budget.tryMetaPrefix || budget.metaCandidates >= len(finalists) {
		return vp8lBuildFinalistPlans(width, height, alpha, finalists, budget, workspace)
	}

	exactBudget := budget
	exactBudget.tryMetaPrefix = false
	exactBudget.entropyIterations = 0
	exactPlans := vp8lBuildFinalistPlans(width, height, alpha, finalists, exactBudget, workspace)
	plans := make([]*vp8lPlan, len(exactPlans))
	indices := make([]int, len(exactPlans))
	for i := range indices {
		indices[i] = i
		plans[i] = vp8lScreenCandidateEntropyPlan(width, height, alpha, finalists[i], exactPlans[i], budget, workspace)
	}
	sort.SliceStable(indices, func(i int, j int) bool {
		return plans[indices[i]].payloadBitLen() < plans[indices[j]].payloadBitLen()
	})
	for _, index := range indices[:minInt(budget.metaCandidates, len(indices))] {
		plans[index] = vp8lRefineCandidateEntropyPlan(width, height, alpha, finalists[index], exactPlans[index], budget, workspace)
	}
	return plans
}

func vp8lScreenCandidateEntropyPlan(width int, height int, alpha bool, candidate vp8lTransformCandidate, seed *vp8lPlan, budget vp8lBudget, workspace *vp8lSearchWorkspace) *vp8lPlan {
	screenBudget := budget
	screenBudget.metaPrefixBits = []uint8{5}
	screenBudget.maxEntropyGroups = 2
	screenBudget.entropyRefinements = 0
	screenBudget.entropyIterations = 0
	image := vp8lChooseEntropyPlanWorkspace(seed.image, screenBudget, workspace)
	return newVP8LPlanForImage(width, height, alpha, candidate.transforms, image)
}

func vp8lRefineCandidateEntropyPlan(width int, height int, alpha bool, candidate vp8lTransformCandidate, seed *vp8lPlan, budget vp8lBudget, workspace *vp8lSearchWorkspace) *vp8lPlan {
	image := vp8lChooseEntropyPlanWorkspace(seed.image, budget, workspace)
	if image.meta != nil && budget.entropyIterations > 0 {
		pixels := candidate.pixels
		if workspace != nil && candidate.materializeWorkspace != nil {
			pixels = candidate.materializeWorkspace(&workspace.transform, 0)
		} else if candidate.materialize != nil {
			pixels = candidate.materialize()
		}
		graph := buildVP8LMatchGraphWorkspace(pixels, candidate.width, budget, workspace)
		image = vp8lReparseEntropyPlanWorkspace(pixels, graph, image, budget, workspace)
	}
	if budget.refineFinalEntropyGroups {
		image = vp8lRefineFinalEntropyGroupsWorkspace(image, budget, workspace)
	}
	return newVP8LPlanForImage(width, height, alpha, candidate.transforms, image)
}
