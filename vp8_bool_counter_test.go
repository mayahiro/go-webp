package webp

import (
	"slices"
	"testing"
)

func TestVP8BoolCounterSizeMatchesEncoder(t *testing.T) {
	for _, bitCount := range []int{0, 1, 7, 8, 23, 24, 25, 255, 4096, 16384} {
		encoder := newVP8BoolEncoder()
		counter := newVP8BoolCounter()
		state := uint32(0x9e3779b9)
		for i := 0; i < bitCount; i++ {
			state = state*1664525 + 1013904223
			bit := state&(1<<31) != 0
			if i%5 == 0 {
				encoder.writeBitEqualProb(bit)
				counter.writeBitEqualProb(bit)
				continue
			}
			prob := uint8(state >> 24)
			encoder.writeBit(prob, bit)
			counter.writeBit(prob, bit)
		}
		if counter.range_ != encoder.range_ || counter.bottom != encoder.bottom || counter.bitCount != encoder.bitCount {
			t.Fatalf("%d bits: counter state = range:%d bottom:%d bitCount:%d, encoder = range:%d bottom:%d bitCount:%d", bitCount, counter.range_, counter.bottom, counter.bitCount, encoder.range_, encoder.bottom, encoder.bitCount)
		}
		if got, want := counter.size(), len(encoder.bytes()); got != want {
			t.Fatalf("%d bits: counter size = %d, want %d", bitCount, got, want)
		}
	}
}

func TestVP8FirstPartitionSizeMatchesEncodedBytes(t *testing.T) {
	stressSegmentation, stressModes, stressTokenProbs, stressSkipMap := makeVP8FirstPartitionStressPlan(4, 3)
	for _, tc := range []struct {
		name         string
		mbw          int
		mbh          int
		segmentation *vp8Segmentation
		modes        []vp8MBMode
		tokenProbs   vp8TokenProbs
		skipMap      []bool
		skipProb     uint8
	}{
		{
			name: "single-y16",
			mbw:  1,
			mbh:  1,
			modes: []vp8MBMode{{
				useY16: true,
				yMode:  vp8PredDC,
				cMode:  vp8PredTM,
			}},
			tokenProbs: vp8DefaultTokenProbs,
		},
		{
			name:         "y4-segment-skip-token-update",
			mbw:          4,
			mbh:          3,
			segmentation: stressSegmentation,
			modes:        stressModes,
			tokenProbs:   stressTokenProbs,
			skipMap:      stressSkipMap,
			skipProb:     128,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const qIndex = 30
			quantDeltas := vp8QuantDeltas{y1DC: -3, y2DC: 4, y2AC: -5, uvDC: -2, uvAC: 6}
			filter := vp8LoopFilterForIndex(qIndex)
			got := vp8FirstPartitionSize(tc.mbw, tc.mbh, qIndex, quantDeltas, filter, tc.segmentation, tc.modes, tc.tokenProbs, tc.skipMap, tc.skipProb)
			encoded, err := vp8FirstPartition(tc.mbw, tc.mbh, qIndex, quantDeltas, filter, tc.segmentation, tc.modes, tc.tokenProbs, tc.skipMap, tc.skipProb)
			if err != nil {
				t.Fatalf("vp8FirstPartition failed: %v", err)
			}
			if got != len(encoded) {
				t.Fatalf("size = %d, encoded bytes = %d", got, len(encoded))
			}
		})
	}
}

func TestVP8FirstPartitionSizeNearLimit(t *testing.T) {
	const maxMacroblockDimension = (maxVP8Dimension + 15) >> 4
	const qIndex = 30
	quantDeltas := vp8QuantDeltas{uvDC: -2}
	filter := vp8LoopFilterForIndex(qIndex)

	firstOverflowingRows := 1
	for low, high := 1, maxMacroblockDimension; low <= high; {
		middle := low + (high-low)/2
		segmentation, modes, tokenProbs, skipMap := makeVP8FirstPartitionStressPlan(maxMacroblockDimension, middle)
		size := vp8FirstPartitionSize(maxMacroblockDimension, middle, qIndex, quantDeltas, filter, segmentation, modes, tokenProbs, skipMap, 128)
		if size > vp8FirstPartitionMax {
			firstOverflowingRows = middle
			high = middle - 1
		} else {
			low = middle + 1
		}
	}

	lastFittingColumns := 0
	for low, high := 1, maxMacroblockDimension; low <= high; {
		middle := low + (high-low)/2
		segmentation, modes, tokenProbs, skipMap := makeVP8FirstPartitionStressPlan(middle, firstOverflowingRows)
		size := vp8FirstPartitionSize(middle, firstOverflowingRows, qIndex, quantDeltas, filter, segmentation, modes, tokenProbs, skipMap, 128)
		if size <= vp8FirstPartitionMax {
			lastFittingColumns = middle
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	if lastFittingColumns == 0 || lastFittingColumns >= maxMacroblockDimension {
		t.Fatalf("last fitting columns = %d, want an interior value", lastFittingColumns)
	}

	segmentation, modes, tokenProbs, skipMap := makeVP8FirstPartitionStressPlan(lastFittingColumns, firstOverflowingRows)
	estimated := vp8FirstPartitionSize(lastFittingColumns, firstOverflowingRows, qIndex, quantDeltas, filter, segmentation, modes, tokenProbs, skipMap, 128)
	if remaining := vp8FirstPartitionMax - estimated; remaining < 0 || remaining > 4096 {
		t.Fatalf("estimated size = %d, want within 4096 bytes below limit %d", estimated, vp8FirstPartitionMax)
	}
	encoded, err := vp8FirstPartition(lastFittingColumns, firstOverflowingRows, qIndex, quantDeltas, filter, segmentation, modes, tokenProbs, skipMap, 128)
	if err != nil {
		t.Fatalf("near-limit vp8FirstPartition failed: %v", err)
	}
	if estimated != len(encoded) {
		t.Fatalf("near-limit size = %d, encoded bytes = %d", estimated, len(encoded))
	}

	overflowSegmentation, overflowModes, overflowTokenProbs, overflowSkipMap := makeVP8FirstPartitionStressPlan(lastFittingColumns+1, firstOverflowingRows)
	overflowSize := vp8FirstPartitionSize(lastFittingColumns+1, firstOverflowingRows, qIndex, quantDeltas, filter, overflowSegmentation, overflowModes, overflowTokenProbs, overflowSkipMap, 128)
	if overflowSize <= vp8FirstPartitionMax {
		t.Fatalf("next plan size = %d, want greater than %d", overflowSize, vp8FirstPartitionMax)
	}
	t.Logf("near-limit plan = %dx%d macroblocks, %d bytes; next column = %d bytes", lastFittingColumns, firstOverflowingRows, estimated, overflowSize)
	if _, err := vp8FirstPartition(lastFittingColumns+1, firstOverflowingRows, qIndex, quantDeltas, filter, overflowSegmentation, overflowModes, overflowTokenProbs, overflowSkipMap, 128); err == nil {
		t.Fatal("overflowing first partition succeeded")
	}
}

func makeVP8FirstPartitionStressPlan(mbw int, mbh int) (*vp8Segmentation, []vp8MBMode, vp8TokenProbs, []bool) {
	macroblocks := mbw * mbh
	segmentation := &vp8Segmentation{
		count:    vp8SegmentCount,
		mapIDs:   make([]uint8, macroblocks),
		mapProbs: [3]uint8{128, 128, 128},
	}
	for i := range segmentation.segments {
		segmentation.segments[i] = vp8SegmentConfig{
			quant:       vp8QuantForIndex(20 + i*10),
			filterLevel: 4 + i,
		}
	}
	modes := make([]vp8MBMode, macroblocks)
	skipMap := make([]bool, macroblocks)
	for macroblock := range modes {
		segmentation.mapIDs[macroblock] = uint8(macroblock % vp8SegmentCount)
		modes[macroblock].cMode = vp8PredTM
		for block := range modes[macroblock].y4Modes {
			modes[macroblock].y4Modes[block] = vp8PredHU
		}
		skipMap[macroblock] = macroblock&1 != 0
	}
	tokenProbs := vp8DefaultTokenProbs
	for plane := range tokenProbs {
		for band := range tokenProbs[plane] {
			for context := range tokenProbs[plane][band] {
				for node := range tokenProbs[plane][band][context] {
					if tokenProbs[plane][band][context][node] == 1 {
						tokenProbs[plane][band][context][node] = 2
					} else {
						tokenProbs[plane][band][context][node] = 1
					}
				}
			}
		}
	}
	return segmentation, modes, tokenProbs, skipMap
}

func BenchmarkVP8FirstPartitionSizeNearLimit(b *testing.B) {
	const mbw, mbh = 1020, 108
	segmentation, modes, tokenProbs, skipMap := makeVP8FirstPartitionStressPlan(mbw, mbh)
	filter := vp8LoopFilterForIndex(30)
	b.ReportAllocs()
	for b.Loop() {
		if got := vp8FirstPartitionSize(mbw, mbh, 30, vp8QuantDeltas{uvDC: -2}, filter, segmentation, modes, tokenProbs, skipMap, 128); got == 0 {
			b.Fatal("zero size")
		}
	}
}

func TestVP8FirstPartitionSizeDoesNotMutatePlan(t *testing.T) {
	segmentation, modes, tokenProbs, skipMap := makeVP8FirstPartitionStressPlan(8, 8)
	wantModes := append([]vp8MBMode(nil), modes...)
	wantSegmentMap := append([]uint8(nil), segmentation.mapIDs...)
	wantSkipMap := append([]bool(nil), skipMap...)
	_ = vp8FirstPartitionSize(8, 8, 30, vp8QuantDeltas{uvDC: -2}, vp8LoopFilterForIndex(30), segmentation, modes, tokenProbs, skipMap, 128)
	if !slices.Equal(modes, wantModes) {
		t.Fatal("size estimation mutated modes")
	}
	if !slices.Equal(segmentation.mapIDs, wantSegmentMap) {
		t.Fatal("size estimation mutated segment map")
	}
	if !slices.Equal(skipMap, wantSkipMap) {
		t.Fatal("size estimation mutated skip map")
	}
}
