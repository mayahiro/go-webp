package webp

import (
	"bytes"
	"image"
	"testing"
)

func TestVP8LumaTargetRange(t *testing.T) {
	var target lumaTargetBlocks
	for block := range target {
		for i := range target[block] {
			target[block][i] = 90
		}
	}
	if got := vp8LumaTargetRange(&target); got != 0 {
		t.Fatalf("flat range = %d, want 0", got)
	}
	target[3][7] = 94
	target[12][9] = 87
	if got := vp8LumaTargetRange(&target); got != 7 {
		t.Fatalf("non-flat range = %d, want 7", got)
	}
}

func TestVP8Y4FlatnessGateProfiles(t *testing.T) {
	if got := vp8LossyConfigForModeQuality(ModeDefault, 75).y4FlatnessLimit; got != 1 {
		t.Fatalf("Default flatness limit = %d, want 1", got)
	}
	if got := vp8LossyConfigForModeQuality(ModeBestCompression, 75).y4FlatnessLimit; got != 0 {
		t.Fatalf("BestCompression flatness limit = %d, want 0", got)
	}
	variants := lossyAblationVariantsForExperiment(75, "y4-flatness-full")
	if len(variants) != 2 || variants[0].config.y4FlatnessLimit != 0 || variants[1].config.y4FlatnessLimit != 1 {
		t.Fatalf("flatness variants = %#v", variants)
	}
}

func TestVP8Y4RefinementBeamKeepsGreedyAndCommitsWinner(t *testing.T) {
	const stride = 32
	bounds := image.Rect(0, 0, stride, 32)
	cfg := vp8LossyConfigForModeQuality(ModeBestCompression, 75)
	probs := vp8DefaultTokenProbs
	quant := cfg.quant.withTrellis(&probs)
	quant.trellisPasses = 1
	strictImprovement := false
	for seed := uint32(1); seed <= 32; seed++ {
		state := seed
		nextByte := func() uint8 {
			state = state*1664525 + 1013904223
			return uint8(state >> 24)
		}
		initialRec := make([]uint8, stride*32)
		for i := range initialRec {
			initialRec[i] = nextByte()
		}
		var target lumaTargetBlocks
		for block := range target {
			for i := range target[block] {
				target[block][i] = nextByte()
			}
		}
		initialLeftPred := [4]uint8{nextByte() % vp8NumPredModes, nextByte() % vp8NumPredModes, nextByte() % vp8NumPredModes, nextByte() % vp8NumPredModes}
		initialUpPred := [4]uint8{nextByte() % vp8NumPredModes, nextByte() % vp8NumPredModes, nextByte() % vp8NumPredModes, nextByte() % vp8NumPredModes}
		initialLeftNZ := [4]uint8{nextByte() & 1, nextByte() & 1, nextByte() & 1, nextByte() & 1}
		initialUpNZ := [4]uint8{nextByte() & 1, nextByte() & 1, nextByte() & 1, nextByte() & 1}

		greedyRec := bytes.Clone(initialRec)
		greedyLeftPred, greedyUpPred := initialLeftPred, initialUpPred
		greedyLeftNZ, greedyUpNZ := initialLeftNZ, initialUpNZ
		var greedyMode vp8MBMode
		greedyScore, _ := chooseVP8Y4Modes(&target, 1, 1, greedyRec, stride, quant, cfg.rd, &probs, nil, &greedyLeftPred, &greedyUpPred, &greedyLeftNZ, &greedyUpNZ, &greedyMode)

		beamRec := bytes.Clone(initialRec)
		beamLeftPred, beamUpPred := initialLeftPred, initialUpPred
		beamLeftNZ, beamUpNZ := initialLeftNZ, initialUpNZ
		var beamMode vp8MBMode
		var beamResiduals vp8MacroblockResiduals
		beamScore, _ := chooseVP8Y4ModesBeam(&target, 1, 1, beamRec, stride, quant, cfg.rd, &probs, &beamResiduals, &beamLeftPred, &beamUpPred, &beamLeftNZ, &beamUpNZ, &beamMode, 2)
		if beamScore > greedyScore {
			t.Fatalf("seed %d beam score = %d, greedy = %d", seed, beamScore, greedyScore)
		}
		strictImprovement = strictImprovement || beamScore < greedyScore

		referenceRec := bytes.Clone(initialRec)
		referenceLeftNZ, referenceUpNZ := initialLeftNZ, initialUpNZ
		var referenceResiduals vp8MacroblockResiduals
		readLuma := func(x int, y int) uint8 {
			block := ((y-16)/4)*4 + (x-16)/4
			pixel := ((y-16)%4)*4 + (x-16)%4
			return target[block][pixel]
		}
		sink := vp8ResidualSink{macroblock: &referenceResiduals}
		processVP8Luma4MB(readLuma, bounds, 1, 1, referenceRec, stride, quant, &referenceLeftNZ, &referenceUpNZ, beamMode, &sink)
		if !bytes.Equal(beamRec, referenceRec) {
			t.Fatalf("seed %d beam reconstruction does not match committed modes", seed)
		}
		if beamLeftNZ != referenceLeftNZ || beamUpNZ != referenceUpNZ {
			t.Fatalf("seed %d beam non-zero context does not match committed modes", seed)
		}
		if beamResiduals != referenceResiduals {
			t.Fatalf("seed %d beam residuals do not match committed modes", seed)
		}
		for by := 0; by < 4; by++ {
			if beamLeftPred[by] != beamMode.y4Modes[by*4+3] {
				t.Fatalf("seed %d left mode row %d = %d, want %d", seed, by, beamLeftPred[by], beamMode.y4Modes[by*4+3])
			}
		}
		for bx := 0; bx < 4; bx++ {
			if beamUpPred[bx] != beamMode.y4Modes[12+bx] {
				t.Fatalf("seed %d top mode column %d = %d, want %d", seed, bx, beamUpPred[bx], beamMode.y4Modes[12+bx])
			}
		}
	}
	if !strictImprovement {
		t.Fatal("beam did not improve any deterministic fixture")
	}
}
