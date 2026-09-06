package webp

import (
	"fmt"
	"slices"
	"testing"
)

func TestVP8LOptimalTokensChangingGroups(t *testing.T) {
	groups := vp8lParseTestGroups()
	pixels := make([]uint32, 12)
	for i := range pixels {
		pixels[i] = 0xffffffff
	}
	graph := vp8lParseTestGraph(len(pixels), func(position int) []vp8lMatch {
		if position < 2 || len(pixels)-position < 4 {
			return nil
		}
		return []vp8lMatch{vp8lNewMatch(min(6, len(pixels)-position), 1), vp8lNewMatch(4, 2)}
	})
	var workspace vp8lSearchWorkspace
	for _, span := range []int{1, 2, 3, len(pixels)} {
		for offset := range len(groups) {
			for _, cached := range []bool{false, true} {
				t.Run(fmt.Sprintf("span%d/offset%d/cache%t", span, offset, cached), func(t *testing.T) {
					groupAt := func(position int) *vp8lCodeGroup { return &groups[(position/span+offset)%len(groups)] }
					var hits []int32
					if cached {
						hits = make([]int32, len(pixels))
						for i := range hits {
							hits[i] = int32(i%3 - 1)
						}
					}
					tokens := vp8lOptimalTokensWithGroups(pixels, graph, hits, groupAt, &workspace)
					// Enumerate every path for this small graph without a cost cache
					// or DP table, including every legal copy length up to six.
					var bestCost func(int) uint64
					bestCost = func(position int) uint64 {
						if position == len(pixels) {
							return 0
						}
						group := groupAt(position)
						best := group.literalCost(pixels[position]) + bestCost(position+1)
						if len(hits) != 0 && hits[position] >= 0 {
							best = min(best, group.green.symbolCost(nLiteralCodes+nLengthCodes+int(hits[position]), 12)+bestCost(position+1))
						}
						for _, match := range graph.at(position) {
							for length := 4; length <= int(match.length); length++ {
								best = min(best, vp8lParseTestCopyCost(group, length, int(match.distanceCode))+bestCost(position+length))
							}
						}
						return best
					}
					want := bestCost(0)
					var actual uint64
					position := 0
					for _, token := range tokens {
						group := groupAt(position)
						switch token.kind() {
						case vp8lTokenLiteral:
							actual += group.literalCost(token.literal())
						case vp8lTokenCache:
							actual += group.green.symbolCost(nLiteralCodes+nLengthCodes+token.cacheIndex(), 12)
						case vp8lTokenCopy:
							actual += vp8lParseTestCopyCost(group, token.copyLength(), token.distanceCode())
						}
						position += vp8lTokenPixelLength(token)
					}
					if position != len(pixels) || actual != want || workspace.dpCosts[len(pixels)] != want {
						t.Fatalf("covered %d pixels, token cost %d, DP cost %d; want %d pixels, cost %d", position, actual, workspace.dpCosts[len(pixels)], len(pixels), want)
					}
				})
			}
		}
	}
}

func TestVP8LOptimalTokensCopyBoundaries(t *testing.T) {
	groups := vp8lParseTestGroups()
	var workspace vp8lSearchWorkspace
	for _, length := range []int{4, 5, 6, 8, 9, 16, 17, 24, 32, 33, 64, 65, 128, 129, 256, 257, 512, 513, 1024, 1025, 2048, 2049, 4096} {
		for _, distance := range []int{1, 2, 4, 5, 8, 9, 16, 17, 64, 65, 120} {
			const start = 120
			pixels := make([]uint32, start+length)
			want := make([]vp8lToken, start+1)
			for i := range pixels {
				pixels[i] = 0xffffffff
			}
			for i := range start {
				want[i] = vp8lLiteralToken(0xffffffff)
			}
			want[start] = vp8lCopyToken(length, distance)
			graph := vp8lParseTestGraph(len(pixels), func(position int) []vp8lMatch {
				if position == start {
					return []vp8lMatch{vp8lNewMatch(length, distance)}
				}
				return nil
			})
			for i := range groups {
				group := &groups[i]
				got := vp8lOptimalTokensWithGroups(pixels, graph, nil, func(int) *vp8lCodeGroup { return group }, &workspace)
				if !slices.Equal(got, want) {
					t.Fatalf("length %d, distance %d, group %d: did not select the full copy", length, distance, i)
				}
				cost := start*group.literalCost(0xffffffff) + vp8lParseTestCopyCost(group, length, distance)
				if workspace.dpCosts[len(pixels)] != cost {
					t.Fatalf("length %d, distance %d, group %d: cost %d, want %d", length, distance, i, workspace.dpCosts[len(pixels)], cost)
				}
			}
		}
	}
}

func vp8lParseTestGroups() []vp8lCodeGroup {
	full8 := vp8lHuffmanTree{kind: vp8lTreeFull8}
	green := make([]uint8, nLiteralCodes+nLengthCodes+2)
	for i := range green {
		green[i] = uint8(1 + i%15)
	}
	distance := make([]uint8, nDistanceCodes)
	for i := range distance {
		distance[i] = uint8(i % 16) // Zero lengths exercise the fallback cost.
	}
	groups := []vp8lCodeGroup{
		{green: vp8lHuffmanTree{kind: vp8lTreeNormal, lengths: green}, distance: vp8lHuffmanTree{kind: vp8lTreeNormal, lengths: distance}},
		{green: full8, distance: vp8lHuffmanTree{kind: vp8lTreeSimple, symbols: [2]uint16{1}, symbolCount: 1}},
		{green: vp8lHuffmanTree{kind: vp8lTreeSimple, symbols: [2]uint16{0, 255}, symbolCount: 2}, distance: vp8lHuffmanTree{kind: vp8lTreeSimple, symbols: [2]uint16{0, 1}, symbolCount: 2}},
		{green: vp8lHuffmanTree{kind: vp8lTreeNormal, lengths: []uint8{1}}, distance: vp8lHuffmanTree{kind: vp8lTreeNormal, lengths: []uint8{0}}},
	}
	for i := range groups {
		groups[i].red, groups[i].blue, groups[i].alpha = full8, full8, full8
	}
	return groups
}

func vp8lParseTestGraph(size int, matches func(int) []vp8lMatch) vp8lMatchGraph {
	graph := vp8lMatchGraph{starts: make([]uint32, size)}
	for position := size - 1; position >= 0; position-- {
		graph.starts[position] = uint32(len(graph.edges))
		graph.edges = append(graph.edges, matches(position)...)
	}
	return graph
}

func vp8lParseTestCopyCost(group *vp8lCodeGroup, length, distance int) uint64 {
	lp, dp := vp8lPrefixCode(length), vp8lDistancePrefixCode(distance)
	return group.green.symbolCost(nLiteralCodes+lp.code, 10) + uint64(lp.extraBits) +
		group.distance.symbolCost(dp.code, 7) + uint64(dp.extraBits)
}
