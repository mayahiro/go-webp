GO ?= go
ARGS ?=
BENCH_ARGS ?=

.PHONY: check test test-root test-benchmarks vet vet-root vet-benchmarks fmt fmt-check tidy tools bench bench-lossy bench-lossless verify-external compare-lossy compare-lossless generate-fixtures index-corpus

check: test vet fmt-check

test: test-root test-benchmarks

test-root:
	$(GO) test ./...

test-benchmarks:
	$(GO) -C benchmarks test ./...

vet: vet-root vet-benchmarks

vet-root:
	$(GO) vet ./...

vet-benchmarks:
	$(GO) -C benchmarks vet ./...

fmt:
	$(GO) -C tools tool goimports -w ..

fmt-check:
	@files="$$($(GO) -C tools tool goimports -l ..)"; \
	status=$$?; \
	if [ "$$status" -ne 0 ]; then \
		exit "$$status"; \
	fi; \
	if [ -n "$$files" ]; then \
		printf '%s\n' "$$files"; \
		exit 1; \
	fi

tidy:
	$(GO) mod tidy
	$(GO) -C tools mod tidy
	$(GO) -C benchmarks mod tidy

tools:
	$(GO) -C tools mod download

bench: bench-lossy bench-lossless

bench-lossy:
	$(GO) -C benchmarks test ./encode -run '^$$' -bench '^BenchmarkEncodeLossyFixtures$$' -benchmem -benchtime=3x -count=3 $(BENCH_ARGS)

bench-lossless:
	$(GO) -C benchmarks test ./encode -run '^$$' -bench '^BenchmarkEncodeLosslessFixtures$$' -benchmem -benchtime=3x -count=3 $(BENCH_ARGS)

verify-external:
	$(GO) run ./scripts/verify_lossless_external $(ARGS)

compare-lossy:
	$(GO) -C benchmarks run ./cmd/compare_lossy_libwebp $(ARGS)

compare-lossless:
	$(GO) -C benchmarks run ./cmd/compare_lossless_libwebp $(ARGS)

generate-fixtures:
	$(GO) -C benchmarks run ./cmd/generate_benchmark_fixtures $(ARGS)

index-corpus:
	$(GO) -C benchmarks run ./cmd/index_benchmark_corpus $(ARGS)
