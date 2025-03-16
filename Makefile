# Define environment variables
SHELL := /usr/bin/env bash
binary_name := nugget
bin_dir := /tmp/bin
pkg := ./...
cover_out := coverage.out
cover_html := coverage.html
cpu_prof := cpu.prof
mem_prof := mem.prof

.PHONY: all audit bench build clean coverage fuzz help lint profile/cpu profile/mem run test tidy

## all: Format, lint, audit, then test
all: tidy lint audit test

## audit: Run static analysis and security checks
audit: test
	@echo "Running audit checks..."
	@go vet $(pkg)
	@go run golang.org/x/vuln/cmd/govulncheck@latest $(pkg)

## bench: Run all benchmark tests
bench:
	@echo "Running benchmarks..."
	@go test -bench=. -benchmem $(pkg)

## build: Build the application binary
build:
	@echo "Building ${binary_name}..."
	@mkdir -p $(bin_dir)
	@go build -race -o=$(bin_dir)/$(binary_name) .

## clean: Remove build and test artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -f $(cpu_prof) $(mem_prof) $(cover_out) $(cover_html) $(binary_name).test
	@rm -f $(bin_dir)/$(binary_name)  # Remove only the project binary, not the whole dir

## coverage: Run tests with coverage and generate a report
coverage:
	@echo "Running tests with coverage..."
	@go test -coverprofile=$(cover_out) $(pkg)
	@go tool cover -html=$(cover_out) -o $(cover_html)
	@echo "Coverage report generated: $(cover_html)"

## fuzz: Run fuzz tests
fuzz:
	@echo "Running fuzz tests..."
	@go test -fuzz=FuzzCache $(pkg)

## help: Print this help message
help:
	@echo "Available make targets:"
	@grep -E '^##' $(MAKEFILE_LIST) | sed 's/^## //' | column -t -s ':' | sed 's/^/  /'

## lint: Run static analysis and linting
lint:
	@echo "Running linter..."
	@go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run $(pkg)

## profile/cpu: Run CPU profiling with benchmarks
profile/cpu:
	@echo "Running CPU profiling..."
	@go test -bench=. -cpuprofile=$(cpu_prof) -run=^$$ $(pkg)
	@echo "Launching CPU profiler (http://localhost:8080)..."
	@go tool pprof -http=:8080 $(cpu_prof)

## profile/mem: Run memory profiling with benchmarks
profile/mem:
	@echo "Running memory profiling..."
	@go test -bench=. -memprofile=$(mem_prof) -run=^$$ $(pkg)
	@echo "Launching memory profiler (http://localhost:8080)..."
	@go tool pprof -http=:8080 $(mem_prof)

## run: Build and execute the application
run: build
	@echo "Running ${binary_name}..."
	@$(bin_dir)/$(binary_name)

## test: Run tests with race detection and coverage
test:
	@echo "Running tests..."
	go test -race -count=10 -timeout=60s -parallel=4 -cover -coverpkg=github.com/wonbyte/gust $(pkg)

## tidy: Format code and clean up dependencies
tidy:
	@echo "Tidying and formatting..."
	@go mod tidy -v
	@go fmt $(pkg)
