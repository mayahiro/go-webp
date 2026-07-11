package webp

func vp8lForEachPredictorCandidate(
	source []uint32,
	width int,
	height int,
	budget vp8lBudget,
	prefix []vp8lTransform,
	materializeSource func() []uint32,
	materializeSourceWorkspace func(*vp8lTransformWorkspace, int) []uint32,
	workspace *vp8lTransformWorkspace,
	predictorSlot int,
	combinedSlot int,
	visit func(vp8lTransformCandidate),
) {
	counters := budget.counters
	for _, mode := range budget.predictorModes {
		modes, transformWidth, transformHeight := vp8lUniformPredictorImage(width, height, 9, mode)
		pixels := vp8lApplyPredictorWorkspace(source, width, height, 9, modes, transformWidth, workspace, predictorSlot)
		predictor := vp8lPredictorTransform(9, modes, transformWidth, transformHeight)
		transforms := append(append([]vp8lTransform(nil), prefix...), predictor)
		candidate := vp8lTransformCandidate{
			width:      width,
			height:     height,
			pixels:     pixels,
			transforms: transforms,
			materialize: func() []uint32 {
				counters.recordRematerialization(len(source))
				return vp8lApplyPredictor(materializeSource(), width, height, 9, modes, transformWidth)
			},
			materializeWorkspace: func(workspace *vp8lTransformWorkspace, slot int) []uint32 {
				source := materializeSourceWorkspace(workspace, vp8lAlternateTransformSlot(slot))
				counters.recordWorkspaceMaterialization(len(source))
				return vp8lApplyPredictorWorkspace(source, width, height, 9, modes, transformWidth, workspace, slot)
			},
		}
		visit(candidate)
		vp8lVisitCombinedTransformCandidates(candidate, budget, workspace, combinedSlot, visit)
	}

	for _, sizeBits := range budget.predictorSizeBits {
		modes, transformWidth, transformHeight := vp8lChooseBlockPredictors(source, width, height, sizeBits, budget.predictorModes)
		if len(modes) < 2 || vp8lAllBytesEqual(modes) {
			continue
		}
		pixels := vp8lApplyPredictorWorkspace(source, width, height, sizeBits, modes, transformWidth, workspace, predictorSlot)
		predictor := vp8lPredictorTransform(sizeBits, modes, transformWidth, transformHeight)
		transforms := append(append([]vp8lTransform(nil), prefix...), predictor)
		candidate := vp8lTransformCandidate{
			width:      width,
			height:     height,
			pixels:     pixels,
			transforms: transforms,
			materialize: func() []uint32 {
				counters.recordRematerialization(len(source))
				return vp8lApplyPredictor(materializeSource(), width, height, sizeBits, modes, transformWidth)
			},
			materializeWorkspace: func(workspace *vp8lTransformWorkspace, slot int) []uint32 {
				source := materializeSourceWorkspace(workspace, vp8lAlternateTransformSlot(slot))
				counters.recordWorkspaceMaterialization(len(source))
				return vp8lApplyPredictorWorkspace(source, width, height, sizeBits, modes, transformWidth, workspace, slot)
			},
		}
		visit(candidate)
		vp8lVisitCombinedTransformCandidates(candidate, budget, workspace, combinedSlot, visit)
	}
}

func vp8lTransformsContain(transforms []vp8lTransform, kind vp8lTransformKind) bool {
	for _, transform := range transforms {
		if transform.kind == kind {
			return true
		}
	}
	return false
}
