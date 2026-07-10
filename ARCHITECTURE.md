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

The lossy `BestCompression` profile may materialize full-resolution Y, Cb, and
Cr source planes in one bounded allocation. Other profiles keep the direct
readers, and `LowMemory` explicitly avoids the materialized plane. The plane is
limited to 32 MiB so large images fall back to direct readers.

## Lossless VP8L Pipeline

The VP8L planner analyzes source pixels and compares bounded combinations of
predictor, color, subtract-green, color-indexing, color-cache, LZ77, and
spatial prefix-code choices. Candidate costs include transform headers,
prefix-code trees, token data, and image-data overhead.

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
before assembling the VP8 key frame.

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

VP8 reconstruction planes, top-row contexts, the skip map, and the residual
buffer are encode-scoped workspaces reused across analysis passes. Parallel
candidate builders write disjoint result slots, and candidate reduction remains
ordered, so worker scheduling does not affect the selected bitstream.

Candidate selection and output are deterministic. The project remains pure Go
and does not depend on libwebp or cgo at runtime.
