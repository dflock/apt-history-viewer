BIN     := apt-history-viewer
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
ARCHES  := amd64 arm64
GOFLAGS := -trimpath
LDFLAGS := -s -w -X main.version=$(VERSION)

export CGO_ENABLED := 0

.PHONY: build test check release clean

## build: static binary for this machine in ./apt-history-viewer
build:
	go build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BIN) ./cmd/$(BIN)

## test: vet and run all tests (node, if installed, runs the JS tests)
test:
	go vet ./...
	go test ./...

## check: tests plus a short fuzz of each parser
check: test
	go test -run=^$$ -fuzz=FuzzParse -fuzztime=10s ./internal/apthistory
	go test -run=^$$ -fuzz=FuzzParse -fuzztime=10s ./internal/dpkglog
	go test -run=^$$ -fuzz=FuzzParse -fuzztime=10s ./internal/aptitudelog
	go test -run=^$$ -fuzz=FuzzParseStatus -fuzztime=10s ./internal/dpkgstate
	go test -run=^$$ -fuzz=FuzzBuild -fuzztime=10s ./internal/model

## release: static linux binaries for each arch in dist/, with SHA256SUMS
release: test
	rm -rf dist && mkdir -p dist
	for arch in $(ARCHES); do \
		GOOS=linux GOARCH=$$arch go build $(GOFLAGS) -ldflags="$(LDFLAGS)" \
			-o dist/$(BIN)-$(VERSION)-linux-$$arch ./cmd/$(BIN) || exit 1; \
	done
	cd dist && sha256sum $(BIN)-* > SHA256SUMS
	@ls -l dist

clean:
	rm -rf dist $(BIN)
