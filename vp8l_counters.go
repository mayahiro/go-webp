package webp

import "sync/atomic"

type vp8lSearchCounters struct {
	generatedCandidates   atomic.Uint64
	sampledCandidates     atomic.Uint64
	fullScoredCandidates  atomic.Uint64
	exactFinalists        atomic.Uint64
	materializedPlanes    atomic.Uint64
	rematerializedBytes   atomic.Uint64
	transformedPixels     atomic.Uint64
	huffmanCostBuilds     atomic.Uint64
	huffmanEmissionBuilds atomic.Uint64
	matchChainProbes      atomic.Uint64
	matchEdges            atomic.Uint64
	dpRelaxations         atomic.Uint64
	cacheFullScans        atomic.Uint64
	entropyTrials         atomic.Uint64
	entropyTileCount      atomic.Uint64
	workerCount           atomic.Uint64
}

type vp8lSearchCounterSnapshot struct {
	generatedCandidates   uint64
	sampledCandidates     uint64
	fullScoredCandidates  uint64
	exactFinalists        uint64
	materializedPlanes    uint64
	rematerializedBytes   uint64
	transformedPixels     uint64
	huffmanCostBuilds     uint64
	huffmanEmissionBuilds uint64
	matchChainProbes      uint64
	matchEdges            uint64
	dpRelaxations         uint64
	cacheFullScans        uint64
	entropyTrials         uint64
	entropyTileCount      uint64
	workerCount           uint64
}

func (c *vp8lSearchCounters) snapshot() vp8lSearchCounterSnapshot {
	if c == nil {
		return vp8lSearchCounterSnapshot{}
	}
	return vp8lSearchCounterSnapshot{
		generatedCandidates:   c.generatedCandidates.Load(),
		sampledCandidates:     c.sampledCandidates.Load(),
		fullScoredCandidates:  c.fullScoredCandidates.Load(),
		exactFinalists:        c.exactFinalists.Load(),
		materializedPlanes:    c.materializedPlanes.Load(),
		rematerializedBytes:   c.rematerializedBytes.Load(),
		transformedPixels:     c.transformedPixels.Load(),
		huffmanCostBuilds:     c.huffmanCostBuilds.Load(),
		huffmanEmissionBuilds: c.huffmanEmissionBuilds.Load(),
		matchChainProbes:      c.matchChainProbes.Load(),
		matchEdges:            c.matchEdges.Load(),
		dpRelaxations:         c.dpRelaxations.Load(),
		cacheFullScans:        c.cacheFullScans.Load(),
		entropyTrials:         c.entropyTrials.Load(),
		entropyTileCount:      c.entropyTileCount.Load(),
		workerCount:           c.workerCount.Load(),
	}
}

func (c *vp8lSearchCounters) recordGeneratedCandidate(pixelCount int, transformed bool) {
	if c == nil {
		return
	}
	c.generatedCandidates.Add(1)
	if transformed {
		c.materializedPlanes.Add(1)
		c.transformedPixels.Add(uint64(pixelCount))
	}
}

func (c *vp8lSearchCounters) recordScreening(full bool) {
	if c == nil {
		return
	}
	c.sampledCandidates.Add(1)
	if full {
		c.fullScoredCandidates.Add(1)
		c.huffmanCostBuilds.Add(5)
	}
}

func (c *vp8lSearchCounters) recordExactFinalists(count int) {
	if c != nil {
		c.exactFinalists.Add(uint64(count))
	}
}

func (c *vp8lSearchCounters) recordRematerialization(pixelCount int) {
	if c == nil {
		return
	}
	c.materializedPlanes.Add(1)
	c.rematerializedBytes.Add(uint64(pixelCount) * 4)
	c.transformedPixels.Add(uint64(pixelCount))
}

func (c *vp8lSearchCounters) recordWorkspaceMaterialization(pixelCount int) {
	if c == nil {
		return
	}
	c.materializedPlanes.Add(1)
	c.transformedPixels.Add(uint64(pixelCount))
}

func (c *vp8lSearchCounters) recordHuffmanEmissionBuilds(count int) {
	if c != nil {
		c.huffmanEmissionBuilds.Add(uint64(count))
	}
}

func (c *vp8lSearchCounters) recordHuffmanCostBuilds(count int) {
	if c != nil {
		c.huffmanCostBuilds.Add(uint64(count))
	}
}

func (c *vp8lSearchCounters) recordMatchGraph(probes int, edges int) {
	if c == nil {
		return
	}
	c.matchChainProbes.Add(uint64(probes))
	c.matchEdges.Add(uint64(edges))
}

func (c *vp8lSearchCounters) recordDPRelaxations(count int) {
	if c != nil {
		c.dpRelaxations.Add(uint64(count))
	}
}

func (c *vp8lSearchCounters) recordCacheFullScan() {
	if c != nil {
		c.cacheFullScans.Add(1)
	}
}

func (c *vp8lSearchCounters) recordEntropyTiles(count int) {
	if c != nil {
		c.entropyTileCount.Add(uint64(count))
	}
}

func (c *vp8lSearchCounters) recordEntropyTrial() {
	if c != nil {
		c.entropyTrials.Add(1)
	}
}

func (c *vp8lSearchCounters) recordWorkers(count int) {
	if c == nil {
		return
	}
	workers := uint64(count)
	for current := c.workerCount.Load(); current < workers; current = c.workerCount.Load() {
		if c.workerCount.CompareAndSwap(current, workers) {
			return
		}
	}
}
