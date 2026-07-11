package webp

func vp8lExpandSelectedCombinedCandidates(reservoir *vp8lCandidateReservoir, width int, height int, budget vp8lBudget, workspace *vp8lTransformWorkspace, visit func(vp8lTransformCandidate)) {
	withoutCombined := budget
	withoutCombined.tryCombined = false
	for _, base := range reservoir.familyCandidates(vp8lTransformKindsFamily(vp8lTransformSubtractGreen), 1) {
		pixels := vp8lMaterializeCandidateWorkspace(base, workspace, 0)
		vp8lForEachPredictorCandidate(pixels, width, height, withoutCombined, base.transforms, base.materialize, base.materializeWorkspace, workspace, 1, 2, visit)
	}

	subtractOnly := budget
	subtractOnly.tryColor = false
	for _, base := range reservoir.familyCandidates(vp8lTransformKindsFamily(vp8lTransformPredictor), 2) {
		base.pixels = vp8lMaterializeCandidateWorkspace(base, workspace, 0)
		vp8lVisitCombinedTransformCandidates(base, subtractOnly, workspace, 1, visit)
	}

	if !budget.tryColor {
		return
	}
	colorFamilies := [...]uint32{
		vp8lTransformKindsFamily(vp8lTransformPredictor),
		vp8lTransformKindsFamily(vp8lTransformSubtractGreen, vp8lTransformPredictor),
	}
	for _, family := range colorFamilies {
		for _, base := range reservoir.familyCandidates(family, 1) {
			pixels := vp8lMaterializeCandidateWorkspace(base, workspace, 0)
			vp8lForEachColorTransformCandidate(pixels, base.width, base.height, budget, base.transforms, base.materialize, base.materializeWorkspace, workspace, 1, visit)
		}
	}
}
