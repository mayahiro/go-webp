package webp

import (
	"image"
	"image/color"
)

type vp8lLZ77Source struct {
	readPixel  pixelReader
	bounds     image.Rectangle
	width      int
	height     int
	total      int
	packed     []uint32
	matchGraph vp8lMatchGraph
}

func vp8lPrepareLZ77Source(readPixel pixelReader, bounds image.Rectangle, width int, height int, plan vp8lEncodingPlan, materialize bool, workspace *vp8lLZ77Workspace) vp8lLZ77Source {
	mainWidth, mainHeight := vp8lPlanImageDimensions(width, height, plan)
	mainBounds := image.Rect(0, 0, mainWidth, mainHeight)
	if !plan.colorIndexing {
		mainBounds = bounds
	}
	read := vp8lPlanPixelReader(readPixel, bounds, width, height, plan)
	source := vp8lLZ77Source{
		readPixel: read,
		bounds:    mainBounds,
		width:     mainWidth,
		height:    mainHeight,
		total:     mainWidth * mainHeight,
	}
	if !materialize || workspace == nil || uint64(source.total) > vp8lSourcePlaneMaxBytes/4 {
		return source
	}
	packed := workspace.resetCandidatePixels(source.total)
	for pos := range packed {
		packed[pos] = vp8lPackPixel(vp8lPixelAt(read, mainBounds, mainWidth, pos))
	}
	source.packed = packed
	source.readPixel = vp8lPackedPixelReader(packed, mainBounds, mainWidth)
	return source
}

func (s *vp8lLZ77Source) prepareMatchGraph(plan vp8lEncodingPlan, cfg vp8lEncodingConfig, workspace *vp8lLZ77Workspace) {
	if !cfg.useLZ77MatchGraph || cfg.lz77CostOnly || cfg.optimalLZ77Passes == 0 || len(s.packed) != s.total || s.total < vp8lMatchGraphMinPixels || plan.colorIndexing && len(plan.colorTable) <= 8 {
		return
	}
	candidateCounts := vp8lLZ77CandidateCounts(s.total)
	graph, ok := workspace.buildMatchGraph(s.packed, s.width, candidateCounts)
	if ok {
		s.matchGraph = graph
	}
}

func vp8lPackedPixelReader(pixels []uint32, bounds image.Rectangle, width int) pixelReader {
	return func(x int, y int) color.NRGBA {
		return vp8lUnpackPixel(pixels[(y-bounds.Min.Y)*width+x-bounds.Min.X])
	}
}
