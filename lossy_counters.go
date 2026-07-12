package webp

type lossyCounterKind uint8

const (
	lossyCounterSourcePixelReads lossyCounterKind = iota
	lossyCounterRGBToYUVConversions
	lossyCounterYCbCrDirectConversions
	lossyCounterPreparedSourceBytes
	lossyCounterChromaFilterSamples
	lossyCounterSharpChromaCandidates
	lossyCounterMacroblocks
	lossyCounterSegmentsConsidered
	lossyCounterSegmentsSelected
	lossyCounterSegmentMapBits
	lossyCounterY16ModesScored
	lossyCounterY4BlocksConsidered
	lossyCounterY4ModesScored
	lossyCounterY4MacroblocksSelected
	lossyCounterChromaModesScored
	lossyCounterForwardDCTCount
	lossyCounterInverseDCTCount
	lossyCounterRDPasses
	lossyCounterResidualCollectionPasses
	lossyCounterResidualBlocks
	lossyCounterTrellisBlocks
	lossyCounterTrellisCandidateScores
	lossyCounterTrellisPasses
	lossyCounterTrellisCoefficientVisits
	lossyCounterTrellisLevelChanges
	lossyCounterSkipCandidates
	lossyCounterSkippedMacroblocks
	lossyCounterTokenProbUpdatesTested
	lossyCounterTokenProbUpdatesSelected
	lossyCounterFilterCandidates
	lossyCounterSelectedFilterLevel
	lossyCounterAlphaFilters
	lossyCounterAlphaLiterals
	lossyCounterAlphaCopies
	lossyCounterAlphaOptimalRows
	lossyCounterFirstPartitionBits
	lossyCounterFirstPartitionFallbacks
	lossyCounterCount
)

type lossyCounterSnapshot struct {
	SourcePixelReads         uint64 `json:"source_pixel_reads"`
	RGBToYUVConversions      uint64 `json:"rgb_to_yuv_conversions"`
	YCbCrDirectConversions   uint64 `json:"ycbcr_direct_conversions"`
	PreparedSourceBytes      uint64 `json:"prepared_source_bytes"`
	ChromaFilterSamples      uint64 `json:"chroma_filter_samples"`
	SharpChromaCandidates    uint64 `json:"sharp_chroma_candidates"`
	Macroblocks              uint64 `json:"macroblocks"`
	SegmentsConsidered       uint64 `json:"segments_considered"`
	SegmentsSelected         uint64 `json:"segments_selected"`
	SegmentMapBits           uint64 `json:"segment_map_bits"`
	Y16ModesScored           uint64 `json:"y16_modes_scored"`
	Y4BlocksConsidered       uint64 `json:"y4_blocks_considered"`
	Y4ModesScored            uint64 `json:"y4_modes_scored"`
	Y4MacroblocksSelected    uint64 `json:"y4_macroblocks_selected"`
	ChromaModesScored        uint64 `json:"chroma_modes_scored"`
	ForwardDCTCount          uint64 `json:"forward_dct_count"`
	InverseDCTCount          uint64 `json:"inverse_dct_count"`
	RDPasses                 uint64 `json:"rd_passes"`
	ResidualCollectionPasses uint64 `json:"residual_collection_passes"`
	ResidualBlocks           uint64 `json:"residual_blocks"`
	TrellisBlocks            uint64 `json:"trellis_blocks"`
	TrellisCandidateScores   uint64 `json:"trellis_candidate_scores"`
	TrellisPasses            uint64 `json:"trellis_passes"`
	TrellisCoefficientVisits uint64 `json:"trellis_coefficient_visits"`
	TrellisLevelChanges      uint64 `json:"trellis_level_changes"`
	SkipCandidates           uint64 `json:"skip_candidates"`
	SkippedMacroblocks       uint64 `json:"skipped_macroblocks"`
	TokenProbUpdatesTested   uint64 `json:"token_prob_updates_tested"`
	TokenProbUpdatesSelected uint64 `json:"token_prob_updates_selected"`
	FilterCandidates         uint64 `json:"filter_candidates"`
	SelectedFilterLevel      uint64 `json:"selected_filter_level"`
	AlphaFilters             uint64 `json:"alpha_filters"`
	AlphaLiterals            uint64 `json:"alpha_literals"`
	AlphaCopies              uint64 `json:"alpha_copies"`
	AlphaOptimalRows         uint64 `json:"alpha_optimal_rows"`
	FirstPartitionBits       uint64 `json:"first_partition_bits"`
	FirstPartitionFallbacks  uint64 `json:"first_partition_fallbacks"`
}
