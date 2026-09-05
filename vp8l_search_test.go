package webp

import (
	"bytes"
	"testing"
)

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

func TestVP8LBestWorkspaceFallbackAvoidsSourceMaterialization(t *testing.T) {
	source := vp8lWorkspaceFallbackSourceForTest()
	for _, mode := range []Mode{ModeDefault, ModeBestCompression} {
		budget := vp8lBudgetForMode(mode)
		if vp8lBufferedSearchBytes(uint64(source.width*source.height), budget) <= budget.maxWorkspaceBytes {
			t.Fatal("fixture must exceed both buffered search workspace limits")
		}
	}
	rowsRead := 0
	readRow := source.readRow
	source.readRow = func(y int, dst []uint32) {
		rowsRead++
		readRow(y, dst)
	}
	streaming, err := vp8lBestStreamingPlan(source)
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := writeLosslessVP8L(&want, streaming); err != nil {
		t.Fatal(err)
	}
	streamingReads := rowsRead
	rowsRead = 0
	plan, err := vp8lPlanForMode(source, ModeBestCompression)
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := writeLosslessVP8L(&got, plan); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want.Bytes()) {
		t.Fatal("workspace fallback changed the streaming output")
	}
	if rowsRead > streamingReads {
		t.Fatalf("fallback read %d rows, direct streaming read %d", rowsRead, streamingReads)
	}
}

func BenchmarkVP8LBestWorkspaceFallback(b *testing.B) {
	source := vp8lWorkspaceFallbackSourceForTest()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := vp8lPlanForMode(source, ModeBestCompression); err != nil {
			b.Fatal(err)
		}
	}
}

func vp8lWorkspaceFallbackSourceForTest() vp8lSource {
	return vp8lSource{
		width:  2048,
		height: 1024,
		readRow: func(_ int, dst []uint32) {
			for x := range dst {
				dst[x] = 0xff000000 | uint32(x+1)*2654435761&0xffffff
			}
		},
	}
}
