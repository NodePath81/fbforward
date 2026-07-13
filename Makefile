FBFORWARD_BIN ?= build/bin/fbforward
FBMEASURE_BIN ?= build/bin/fbmeasure
VERSION ?= dev
LDFLAGS ?= -X github.com/NodePath81/fbforward/internal/version.Version=$(VERSION)

.PHONY: all build build-fbforward build-fbmeasure clean test test-e2e

all: build

build: build-fbforward build-fbmeasure

build-fbforward:
	mkdir -p $(dir $(FBFORWARD_BIN))
	go build -ldflags "$(LDFLAGS)" -o $(FBFORWARD_BIN) ./cmd/fbforward

build-fbmeasure:
	mkdir -p $(dir $(FBMEASURE_BIN))
	go build -ldflags "$(LDFLAGS)" -o $(FBMEASURE_BIN) ./cmd/fbmeasure

test:
	go test ./...

test-e2e:
	GOCACHE=/tmp/fbforward-gocache go test -tags=e2e ./test/e2e

clean:
	rm -rf build/bin
