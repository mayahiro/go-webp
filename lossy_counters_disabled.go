//go:build !webp_lossy_counters

package webp

func countLossyCounter(lossyCounterKind, uint64) {}

func setLossyCounter(lossyCounterKind, uint64) {}

func countLossySkippedMacroblocks([]bool) {}

func countLossyAlphaFilters([4]bool) {}

func countLossyAlphaSymbol(int) {}

func countLossyAlphaCopy() {}

func instrumentLossyPixelReader(read pixelReader) pixelReader {
	return read
}

func instrumentLossyLumaReader(read lumaReader) lumaReader {
	return read
}

func instrumentLossyChromaReader(read chromaReader) chromaReader {
	return read
}
