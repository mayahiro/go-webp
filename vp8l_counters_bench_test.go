package webp

import "testing"

func BenchmarkVP8LSearchStages(b *testing.B) {
	cases := []losslessBenchmarkCase{
		{name: "Gradient128", kind: benchmarkImageGradient, width: 128, height: 128},
		{name: "UI256", kind: benchmarkImageUI, width: 256, height: 256},
		{name: "Flat128", kind: benchmarkImageFlat, width: 128, height: 128},
		{name: "Palette256", width: 256, height: 256, format: benchmarkFixturePaletted},
		{name: "Alpha128", kind: benchmarkImageAlpha, width: 128, height: 128},
		{name: "PhotoLike512", kind: benchmarkImagePhotoLike, width: 512, height: 512},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			img := newLosslessBenchmarkFixtureImage(tc)
			source := newEncoderSource(img)
			var snapshot vp8lSearchCounterSnapshot
			var payloadBits uint64
			for b.Loop() {
				counters := &vp8lSearchCounters{}
				budget := vp8lBudgetForMode(ModeDefault)
				budget.counters = counters
				plan, err := searchVP8L(newVP8LSource(source, source.pixels()), budget)
				if err != nil {
					b.Fatal(err)
				}
				snapshot = counters.snapshot()
				payloadBits = plan.payloadBitLen()
			}
			b.ReportMetric(float64(payloadBits), "payload_bits")
			b.ReportMetric(float64(snapshot.generatedCandidates), "generated_candidates")
			b.ReportMetric(float64(snapshot.sampledCandidates), "sampled_candidates")
			b.ReportMetric(float64(snapshot.fullScoredCandidates), "full_scored_candidates")
			b.ReportMetric(float64(snapshot.exactFinalists), "exact_finalists")
			b.ReportMetric(float64(snapshot.materializedPlanes), "materialized_planes")
			b.ReportMetric(float64(snapshot.rematerializedBytes), "rematerialized_bytes")
			b.ReportMetric(float64(snapshot.transformedPixels), "transformed_pixels")
			b.ReportMetric(float64(snapshot.huffmanCostBuilds), "huffman_cost_builds")
			b.ReportMetric(float64(snapshot.huffmanEmissionBuilds), "huffman_emission_builds")
			b.ReportMetric(float64(snapshot.matchChainProbes), "match_chain_probes")
			b.ReportMetric(float64(snapshot.matchEdges), "match_edges")
			b.ReportMetric(float64(snapshot.dpRelaxations), "dp_relaxations")
			b.ReportMetric(float64(snapshot.cacheFullScans), "cache_full_scans")
			b.ReportMetric(float64(snapshot.entropyTrials), "entropy_trials")
			b.ReportMetric(float64(snapshot.entropyTileCount), "entropy_tile_count")
			b.ReportMetric(float64(snapshot.workerCount), "worker_count")
		})
	}
}

func BenchmarkVP8LBestSearchExtension(b *testing.B) {
	cases := []losslessBenchmarkCase{
		{name: "Gradient64", kind: benchmarkImageGradient, width: 64, height: 64},
		{name: "UI64", kind: benchmarkImageUI, width: 64, height: 64},
		{name: "Palette64", width: 64, height: 64, format: benchmarkFixturePaletted},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			img := newLosslessBenchmarkFixtureImage(tc)
			source := newEncoderSource(img)
			var beforeBest vp8lSearchCounterSnapshot
			var afterBest vp8lSearchCounterSnapshot
			var defaultBits uint64
			var bestBits uint64
			for b.Loop() {
				counters := &vp8lSearchCounters{}
				bestBudget := vp8lBudgetForMode(ModeBestCompression)
				session, err := newVP8LSearchSession(newVP8LSource(source, source.pixels()), bestBudget.maxSourceBytes)
				if err != nil {
					b.Fatal(err)
				}
				session.scorer = newVP8LCandidateScorer()
				defaultBudget := vp8lBudgetForMode(ModeDefault)
				defaultBudget.counters = counters
				defaultPlan, err := session.search(defaultBudget, false)
				if err != nil {
					b.Fatal(err)
				}
				beforeBest = counters.snapshot()
				bestBudget.counters = counters
				bestPlan, err := session.search(bestBudget, true)
				if err != nil {
					b.Fatal(err)
				}
				afterBest = counters.snapshot()
				defaultBits = defaultPlan.payloadBitLen()
				bestBits = bestPlan.payloadBitLen()
			}
			b.ReportMetric(float64(defaultBits), "default_payload_bits")
			b.ReportMetric(float64(bestBits), "best_payload_bits")
			b.ReportMetric(float64(afterBest.generatedCandidates-beforeBest.generatedCandidates), "extension_generated_candidates")
			b.ReportMetric(float64(afterBest.sampledCandidates-beforeBest.sampledCandidates), "extension_sampled_candidates")
			b.ReportMetric(float64(afterBest.fullScoredCandidates-beforeBest.fullScoredCandidates), "extension_full_scored_candidates")
			b.ReportMetric(float64(afterBest.exactFinalists-beforeBest.exactFinalists), "extension_exact_finalists")
			b.ReportMetric(float64(afterBest.transformedPixels-beforeBest.transformedPixels), "extension_transformed_pixels")
		})
	}
}
