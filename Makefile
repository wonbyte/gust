SHELL := /usr/bin/env bash
binary_name := gust
bin_dir := /tmp/bin

.PHONY: audit bench build clean coverage fuzz help lint profile/cpu profile/mem run test tidy

## audit: Run quality control checks
audit: test
	@echo "Running audit checks..."
	@go vet ./...
	@go run golang.org/x/vuln/cmd/govulncheck@latest ./

## bench: Run all benchmark tests
bench:
	@go test -bench=. -benchmem

## build: Build the application
build:
	@echo "Building ${binary_name}..."
	@mkdir -p ${bin_dir}
	@go build -race -o=${bin_dir}/${binary_name} .

## clean: Remove generated files
clean:
	@echo "Cleaning build artifacts..."
	@rm -f cpu.prof mem.prof mem.out cpu.out coverage.out coverage.html gust.test

## coverage: Generate a code coverage report and open it in the browser
coverage:
	@echo "Running tests with coverage..."
	@go test -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated at coverage.html"

## fuzz: Run fuzz tests
fuzz:
	@go test -fuzz=FuzzCache

## help: Print this help message
help:
	@echo 'Usage:'
	@grep -E '^##' $(MAKEFILE_LIST) | sed 's/^## //' | column -t -s ':' | sed 's/^/  /'

## lint: Lint source files
lint:
	go run github.com/golangci/golangci-lint/cmd/golangci-lint@latest run ./...

## profile/cpu: Run benchmarks with CPU profiling enabled and launch pprof
profile/cpu:
	@echo "Running benchmarks with CPU profiling..."
	@go test -bench=. -cpuprofile=cpu.prof -run=^$$ .
	@echo "Launching CPU profiler (http://localhost:8080)..."
	@go tool pprof -http=:8080 cpu.prof

## profile/mem: Run benchmarks with memory profiling enabled and launch pprof
profile/mem:
	@echo "Running benchmarks with memory profiling..."
	@go test -bench=. -memprofile=mem.prof -run=^$$ .
	@echo "Launching memory profiler (http://localhost:8080)..."
	@go tool pprof -http=:8080 mem.prof

## run: Run the application
run: build
	@echo "Running ${binary_name}..."
	@${bin_dir}/${binary_name}

## test: Build and run tests using a persistent static test binary
test:
	@echo "Running tests..."
	@go test -race -count=10 -timeout=60s -parallel=4 -cover -coverpkg=github.com/wonbyte/gust ./...

## tidy: Tidy modfiles and format .go files
tidy:
	@echo "Formatting code..."
	@go mod tidy -v
	@go fmt ./...

