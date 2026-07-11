package webp

import "testing"

func TestVP8LBestSessionReusesDefaultSearchState(t *testing.T) {
	const width, height = 32, 24
	rowsRead := 0
	source := vp8lSource{
		width:  width,
		height: height,
		readRow: func(y int, dst []uint32) {
			rowsRead++
			for x := range dst {
				value := uint32((x*37 + y*53 + x*y*3) & 0xff)
				dst[x] = 0xff000000 | value<<16 | (value*5&0xff)<<8 | value*11&0xff
			}
		},
	}

	bestBudget := vp8lBudgetForMode(ModeBestCompression)
	session, err := newVP8LSearchSession(source, bestBudget.maxSourceBytes)
	if err != nil {
		t.Fatal(err)
	}
	session.scorer = newVP8LCandidateScorer()
	counters := &vp8lSearchCounters{}
	defaultBudget := vp8lBudgetForMode(ModeDefault)
	defaultBudget.counters = counters
	defaultBudget.maxWorkers = 1
	if _, err := session.search(defaultBudget, false); err != nil {
		t.Fatal(err)
	}
	beforeBest := counters.snapshot()
	defaultScores := len(session.scorer.entries)

	bestBudget.counters = counters
	bestBudget.maxWorkers = 1
	if _, err := session.search(bestBudget, true); err != nil {
		t.Fatal(err)
	}
	afterBest := counters.snapshot()

	if rowsRead != height {
		t.Fatalf("source rows read = %d, want %d", rowsRead, height)
	}
	if !session.paletteAnalyzed {
		t.Fatal("palette analysis was not retained in the search session")
	}
	if len(session.reusableKeys) != 1 {
		t.Fatalf("reusable winner keys = %d, want 1", len(session.reusableKeys))
	}
	if len(session.scorer.entries) <= defaultScores {
		t.Fatalf("Best search scores = %d, Default scores %d", len(session.scorer.entries), defaultScores)
	}
	extensionSamples := afterBest.sampledCandidates - beforeBest.sampledCandidates
	extensionFullScores := afterBest.fullScoredCandidates - beforeBest.fullScoredCandidates
	if extensionSamples == 0 || extensionFullScores >= extensionSamples {
		t.Fatalf("Best extension sampled %d candidates and fully scored %d", extensionSamples, extensionFullScores)
	}
}

func TestVP8LUniformSearchSkipsPaletteAnalysis(t *testing.T) {
	const width, height = 8, 8
	source := vp8lSource{
		width:  width,
		height: height,
		readRow: func(_ int, dst []uint32) {
			for x := range dst {
				dst[x] = 0xff102030
			}
		},
	}
	session, err := newVP8LSearchSession(source, vp8lMaxSourceBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.search(vp8lBudgetForMode(ModeDefault), false); err != nil {
		t.Fatal(err)
	}
	if session.paletteAnalyzed {
		t.Fatal("uniform search performed an unused palette analysis")
	}
}
