package webp

import "math/bits"

type vp8lCandidateScorer struct {
	indexes   map[vp8lCandidateKey]int
	entries   []vp8lCandidateScoreEntry
	workspace vp8lHuffmanWorkspace
}

type vp8lCandidateScoreEntry struct {
	key       vp8lCandidateKey
	candidate vp8lTransformCandidate
	score     vp8lCandidateScore
}

type vp8lCandidateKey struct {
	first  uint64
	second uint64
}

func newVP8LCandidateScorer() *vp8lCandidateScorer {
	return &vp8lCandidateScorer{indexes: make(map[vp8lCandidateKey]int)}
}

func (s *vp8lCandidateScorer) score(width int, height int, alpha bool, candidate vp8lTransformCandidate) (vp8lCandidateScoreEntry, bool) {
	key := vp8lTransformCandidateKey(candidate)
	if index, ok := s.indexes[key]; ok {
		return s.entries[index], true
	}
	score := vp8lScoreCandidateWorkspace(width, height, alpha, candidate, &s.workspace)
	stored := candidate
	stored.pixels = nil
	entry := vp8lCandidateScoreEntry{key: key, candidate: stored, score: score}
	s.indexes[key] = len(s.entries)
	s.entries = append(s.entries, entry)
	return entry, false
}

func vp8lTransformCandidateKey(candidate vp8lTransformCandidate) vp8lCandidateKey {
	key := vp8lCandidateKey{first: 1469598103934665603, second: 0x9e3779b97f4a7c15}
	key.add(uint64(candidate.width))
	key.add(uint64(candidate.height))
	key.add(uint64(len(candidate.transforms)))
	for _, transform := range candidate.transforms {
		key.add(uint64(transform.kind))
		key.add(uint64(transform.sizeBits))
		key.add(uint64(transform.paletteSize))
		key.add(uint64(transform.image.width))
		key.add(uint64(transform.image.height))
		key.add(uint64(transform.image.cacheBits))
		key.add(uint64(len(transform.image.tokens)))
		for _, token := range transform.image.tokens {
			key.add(uint64(token))
		}
	}
	return key
}

func (k *vp8lCandidateKey) add(value uint64) {
	k.first ^= value
	k.first *= 1099511628211
	k.second ^= value + 0x9e3779b97f4a7c15
	k.second = bits.RotateLeft64(k.second, 27) * 0x94d049bb133111eb
}
