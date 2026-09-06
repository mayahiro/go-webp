package webp

import "testing"

func TestAlphaBestSpatialMatchOffsets(t *testing.T) {
	for _, tc := range []struct{ offset, code int }{
		{-7, 80}, {-6, 60}, {-5, 44}, {-4, 28}, {-3, 18}, {-2, 10}, {-1, 4},
		{0, 1}, {1, 3}, {2, 9}, {3, 17}, {4, 27}, {5, 43}, {6, 59}, {7, 79}, {8, 102},
	} {
		current, previous := make([]byte, 20), make([]byte, 32)
		for i := range previous {
			previous[i] = byte(i + 1)
		}
		copy(current[16:], previous[16-tc.offset:])
		got := alphaBestSpatialMatch(current, previous, 16, true)
		if want := (alphaSpatialMatch{length: 4, distanceCode: tc.code}); got != want {
			t.Fatalf("offset %d: got %+v, want %+v", tc.offset, got, want)
		}
	}
}

func TestAlphaBestSpatialMatchBoundariesAndTies(t *testing.T) {
	for _, tc := range []struct {
		name              string
		current, previous []byte
		start             int
		hasPrevious       bool
		want              alphaSpatialMatch
	}{
		{name: "no previous", current: []byte{7}, start: 0},
		{name: "empty rows", hasPrevious: true},
		{name: "empty previous", current: []byte{7}, hasPrevious: true},
		{name: "row end", current: []byte{7}, previous: []byte{7}, start: 1, hasPrevious: true},
		{name: "no match", current: []byte{1}, previous: []byte{2}, hasPrevious: true},
		{name: "single column", current: []byte{7}, previous: []byte{7}, hasPrevious: true, want: alphaSpatialMatch{1, 1}},
		{name: "left edge", current: []byte{99}, previous: []byte{0, 1, 2, 3, 4, 5, 6, 99}, hasPrevious: true, want: alphaSpatialMatch{1, 80}},
		{name: "right edge", current: []byte{0, 0, 0, 0, 0, 0, 0, 0, 99}, previous: []byte{99}, start: 8, hasPrevious: true, want: alphaSpatialMatch{1, 102}},
		{name: "smallest code on tie", current: []byte{0, 0, 7}, previous: []byte{0, 7, 0, 7}, start: 2, hasPrevious: true, want: alphaSpatialMatch{1, 3}},
		{name: "longer match wins", current: []byte{0, 0, 0, 0, 7, 8, 9}, previous: []byte{7, 8, 9, 0, 7, 0, 0}, start: 4, hasPrevious: true, want: alphaSpatialMatch{3, 27}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := alphaBestSpatialMatch(tc.current, tc.previous, tc.start, tc.hasPrevious); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestAlphaBestSpatialMatchExhaustiveShortRows(t *testing.T) {
	// Include unequal and empty rows to exercise both clipped boundaries.
	for width := 0; width <= 5; width++ {
		for previousWidth := 0; previousWidth <= 5; previousWidth++ {
			for currentBits := range 1 << width {
				for previousBits := range 1 << previousWidth {
					current, previous := make([]byte, width), make([]byte, previousWidth)
					for i := range current {
						current[i] = byte(currentBits >> i & 1)
					}
					for i := range previous {
						previous[i] = byte(previousBits >> i & 1)
					}
					for start := 0; start <= width; start++ {
						for _, hasPrevious := range []bool{false, true} {
							got := alphaBestSpatialMatch(current, previous, start, hasPrevious)
							want := alphaSpatialMatchReference(current, previous, start, hasPrevious)
							if got != want {
								t.Fatalf("current %v, previous %v, start %d, hasPrevious %t: got %+v, want %+v", current, previous, start, hasPrevious, got, want)
							}
						}
					}
				}
			}
		}
	}
}

func alphaSpatialMatchReference(current, previous []byte, start int, hasPrevious bool) alphaSpatialMatch {
	var best alphaSpatialMatch
	if !hasPrevious {
		return best
	}
	// Scan the canonical distance map independently of the offset lookup table.
	for i, offset := range vp8lDistanceMap {
		previousStart := start - offset.x
		if offset.y != 1 || previousStart < 0 || previousStart >= len(previous) {
			continue
		}
		length := alphaMatchLength(current, previous, start, previousStart)
		if length > best.length {
			best = alphaSpatialMatch{length: length, distanceCode: i + 1}
		}
	}
	return best
}
