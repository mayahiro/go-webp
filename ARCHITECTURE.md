# Architecture

go-webp exposes one public encoding entry point and keeps VP8L lossless and
VP8 lossy encoding as independent pipelines. Shared code validates requests,
adapts `image.Image`, and writes the WebP container; each codec owns its search
and bitstream state.

## Entry Point and Source Boundary

`Encode` validates dimensions and options, creates an immutable
`encoderSource`, and dispatches to the selected codec. `encode.go` contains
the public API and WebP container primitives. `pixel_reader.go` contains the
specialized image readers, while `source.go` defines the shared source types.

Direct readers cover `image.NRGBA`, `image.NRGBA64`, `image.RGBA`,
`image.RGBA64`, `image.Gray`, `image.YCbCr`, `image.Paletted`, and
`image.Uniform`. Other implementations use conversion through
`color.NRGBAModel`.

## Lossless VP8L Pipeline

The lossless pipeline is organized around a row-readable `vp8lSource`, a
mode-specific `vp8lBudget`, and an immutable final `vp8lPlan`.

### Search Profiles and Source Policy

`ModeFast` and `ModeLowMemory` use the streaming encoder directly. They keep
row-sized source state, evaluate a small transform set, and use a bounded
greedy parser without retaining a full-image token graph.

Default and balanced profiles first check a 96 MiB estimated workspace gate.
Eligible inputs are materialized into one packed ARGB plane capped at 32 MiB;
inputs outside either limit fall back to streaming. `ModeBestCompression`
widens transform, match, parse, and entropy budgets and raises the estimated
workspace gate to 192 MiB. Uniform inputs and `image.Paletted` inputs with at
most 16 colors use dedicated bounded searches that avoid the general transform
graph.

`ModeAuto` examines a bounded pixel sample. It selects the streaming fast path
only when a verified low-color palette plan is sufficiently small, selects
the low-memory path for very large inputs, and otherwise uses the balanced
profile.

`ModeBestCompression` first establishes the complete default plan, then
expands the budget in the same search session. The materialized source,
palette analysis, candidate scores, and default winner remain reusable. The
expanded plan replaces the incumbent only when its exact payload is smaller,
making the profile monotonic with respect to the default encoded size.

### Transform Graph and Screening

The buffered search constructs a bounded graph of direct, subtract-green,
predictor, cross-color, palette, and selected combined transforms. Predictor
modes can vary by tile. Default search expands combined transforms only from
parents retained by the first screening stage. `BestCompression` can
additionally evaluate block-adaptive cross-color coefficients.

Transform screening uses exact literal coding cost together with sampled local
and distant match potential. Screening builds cost-only Huffman data and
defers canonical codes and emission metadata. A family-aware reservoir keeps
structurally different candidates instead of retaining only candidates with
the lowest literal entropy.

Default search narrows candidates in bounded stages: cost screening, shallow
greedy parsing, exact match and dynamic-programming parsing, color-cache
selection for the leading candidates, and spatial entropy refinement for the
incumbent. Candidate descriptors remain rematerializable without retaining an
owned full-image plane for every transform.

### Match, Parse, Cache, and Entropy Optimization

Exact finalists use a reverse-built match graph. Repeated runs and previous-row
matches propagate their lengths instead of rescanning the same interval, and a
bounded hash chain supplies more distant alternatives. Default matcher tables
adapt from 8 to 16 hash bits with transformed image size, while
`BestCompression` retains the full 16-bit table. Each compact match edge stores
its distance prefix information and occupies eight bytes.

A dynamic-programming parser compares literals, color-cache references, and
backward references using current Huffman costs. The selected stream may be
reparsed for a bounded number of iterations. Color-cache sizes from 1 through
11 bits are screened together in one source traversal and fully emitted only
for a bounded shortlist.

Spatial entropy optimization builds sparse tile histograms, clusters them into
at most 16 code groups, and compares the complete meta-prefix cost. Default
search applies the full optimization only to its incumbent. The candidate
retains the lowest exact emitted bit count, including side images, tree
headers, symbols, and extra bits.

### Count and Write Kernel

`vp8lBitSink` is shared by size counting and emission. Huffman trees and plans
therefore use the same serialization logic for both operations. A completed
plan records its payload bit length, and the writer verifies that emission
produces exactly that count before flushing the WebP chunk.

Plans own the tokens, transforms, trees, and entropy maps needed for emission.
Search scratch is held in typed encode-scoped workspaces and is never observed
by the writer.

### Determinism and Parallelism

Finalists may be evaluated by a bounded worker pool: inputs below 256x256 use
one worker, while larger inputs can use at most two workers for default search
and four for `BestCompression`. Worker count is further limited by
`GOMAXPROCS` and the parallel workspace budget. Results are written to their
original candidate slots and reduced in stable order, so worker scheduling
does not change the selected bitstream.

### Near-Lossless

Near-lossless encoding applies edge-aware RGB quantization with quality bands
compatible with the documented cwebp behavior and preserves alpha exactly.
Its processed source is encoded by the VP8L streaming path, avoiding a second
full-image search workspace.

## Lossy VP8 Pipeline

The lossy path converts the shared source into a `vp8Source`. Analysis produces
a `vp8FramePlan` containing macroblock prediction modes, skip decisions, token
probabilities, and an optional reusable residual buffer. Partition emission
consumes that plan to write an intra-only VP8 key frame.

Alpha is analyzed independently and, when required, is written in an extended
WebP container beside the VP8 frame. Candidates compare raw and compressed
alpha, all enabled filters, run references, and bounded previous-row spatial
references. `BestCompression` can apply a dynamic-programming parse to the
strongest alpha candidates.

VP8 reconstruction uses a two-macroblock-row ring rather than a full-frame
reconstruction plane. Standard opaque image types skip alpha analysis, and
standard images with alpha can run frame planning and alpha analysis in
parallel.

## Memory and Concurrency

All search state is local to one encode call. The package has no mutable global
encoder pool, and concurrent calls do not share plans or scratch buffers.

VP8L source, worker, parallel, and total-search estimates are explicit fields
of `vp8lBudget`. The estimates gate expensive buffered search; cumulative heap
allocation can still be higher than peak live memory because sequential
candidate metadata, tokens, and coding structures contribute to `B/op`.
Transform chains alternate between encode-scoped scratch buffers when exact
finalists are materialized. Streaming modes retain only row state and
immutable transform metadata.

The project remains pure Go and has no runtime dependency on libwebp, cgo, or
architecture-specific assembly.

## Development Module Boundaries

The repository root is the published encoder module. Root `internal` packages
contain benchmark and evaluation support shared by root tests and benchmark
commands; production encoding does not import them.

The nested `benchmarks` module contains the public-API performance suite,
deterministic fixture generation, corpus tooling, external decoder
verification, and optional libwebp comparison commands. Its local `replace`
directive targets the current root checkout without adding development
dependencies to the published module graph.

The nested `tools` module pins development executables. The root Makefile
coordinates tests, vet, formatting, benchmarks, and optional external checks
across these module boundaries.
