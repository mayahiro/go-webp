# Architecture

go-webp has one public encoding entry point and two independent codec
pipelines. The shared layer validates the request and adapts `image.Image`;
VP8L and VP8 keep their own analysis, planning, and bitstream state.

## Entry Point and Source Boundary

`Encode` validates dimensions and options, creates an immutable
`encoderSource`, and dispatches to the lossless or lossy pipeline. The source
keeps the original bounds and dimensions so internal stages do not pass these
values independently.

Image-type-specific readers provide direct paths for `image.NRGBA`,
`image.RGBA`, `image.Gray`, `image.YCbCr`, `image.Paletted`, and
`image.Uniform`. Other `image.Image` implementations use the standard color
model conversion path.

Lossless balanced profiles may materialize one immutable RGBA pixel plane for
inputs whose native conversion is expensive, including YCbCr, 64-bit color,
and custom image implementations. Lossy `BestCompression` may instead
materialize full-resolution Y, Cb, and Cr source planes. Both paths are bounded
to 32 MiB, and `LowMemory` explicitly keeps direct readers.

## Lossless VP8L Pipeline

The VP8L planner analyzes source pixels and compares bounded combinations of
predictor, cross-color, subtract-green, color-indexing, 1- to 11-bit color
cache, LZ77, and spatial prefix-code choices. Predictor modes and cross-color
coefficients can vary by tile. Channel coding retains full 256-symbol
histograms and compares complete Huffman tree and data costs.

Planning is split into two stages. Literal cost and a bounded greedy LZ77 pass
shortlist structurally different candidates; optimal parsing, color-cache,
and entropy-clustered meta-prefix searches then run only on the finalists.
This keeps the final decision based on emitted bit cost without applying every
expensive post-processing path to every transform candidate.

For each shortlisted LZ77 finalist, balanced profiles may materialize the
transformed stream as one packed pixel plane capped at 32 MiB. Greedy parsing,
optimal parsing, and color-cache analysis then share that plane instead of
recomputing predictor and cross-color residuals. Images with at least 65,536
pixels may also use a compact match graph capped at 32 MiB; smaller or larger
inputs keep the direct bounded matcher.

LZ77 token buffers, hash tables, and optimal-parser arrays belong to one
encode-scoped workspace and are reused across finalists. Screening plans keep
only symbol statistics. The eleven color-cache sizes are analyzed in one
source traversal, while selected plans retain their own immutable tokens.
`BestCompression` can run an additional cache-aware optimal parse whose cost
model compares literals, cache hits, and backward references together.

The selected `vp8lEncodingPlan` is immutable during emission. The VP8L writer
serializes transforms and image data from that plan; it does not repeat the
candidate search. `Fast`, `Balanced`, `BestCompression`, and `LowMemory`
control which candidates and buffered token paths are available.

`BestCompression` may analyze independent base predictor and color-transform
candidates with a bounded four-worker pool. Standard image types support
concurrent direct reads. Custom image types are first copied into an immutable
pixel plane limited to 32 MiB; inputs above that limit stay sequential.

Near-lossless encoding preprocesses RGB with cwebp-compatible quality bands
and four-neighbor edge detection while preserving alpha. A bounded 32 MiB
pixel plane supports the normal multi-pass path; larger images use a direct
single-pass reader with the same maximum-error bound.

## Lossy VP8 Pipeline

The lossy path converts the shared source into a `vp8Source`. Analysis produces
a `vp8FramePlan` containing macroblock prediction modes, skip decisions, token
probabilities, and an optional reusable residual buffer. Partition emission
then consumes that plan to write the first partition and residual partition
before assembling the VP8 key frame. Buffered profiles evaluate skip and
no-skip plans with independently optimized token probabilities and retain the
lower estimated total bit cost.

Alpha is analyzed separately and, when needed, is written in an extended WebP
container alongside the VP8 frame. Opaque standard image types skip alpha
analysis. Alpha candidates compare all four global filters, run references,
and previous-row spatial distances by encoded bit cost. `BestCompression` can
retain a bounded dynamic-programming parse; lower-effort profiles use the
greedy parse or omit spatial candidates. For standard image types, frame
planning and alpha analysis may run concurrently with one worker each.

## Memory and Determinism

Search limits, materialization limits, and residual-buffer limits are explicit
parts of internal mode configuration. `LowMemory` avoids full-frame source and
residual materialization, while `BestCompression` permits more retained state
for broader search.

VP8L finalist planes and match graphs are bounded independently at 32 MiB.
They are sequential scratch state rather than global pools, so concurrent
calls do not retain or share encoder memory. Plans copy only the token stream
that survives candidate reduction.

VP8 reconstruction uses a two-macroblock-row ring: 32 luma rows and 16 rows for
each chroma plane. Top-row contexts, the skip map, and the optional residual
buffer are encode-scoped workspaces reused across analysis passes. Parallel
candidate builders write disjoint result slots, and candidate reduction remains
ordered, so worker scheduling does not affect the selected bitstream.

Candidate selection and output are deterministic. The project remains pure Go
and does not depend on libwebp or cgo at runtime.

## Development Module Boundaries

The repository root is the published encoder module. The nested `benchmarks`
module contains the public-API performance suite, generated comparison
fixtures, corpus tooling, external decoder verification, and optional libwebp
comparison commands. Its local `replace` directive always targets the
current root checkout without adding development dependencies to the published
module graph.

White-box benchmarks and ablation tests that require unexported encoder
configuration remain in the root package. This keeps implementation-specific
hooks out of the public API. The nested `tools` module pins development
executables, and the root Makefile coordinates verification across all three
module boundaries.
