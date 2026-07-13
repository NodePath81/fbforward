FBFORWARD_BIN ?= build/bin/fbforward
BWPROBE_BIN ?= build/bin/bwprobe
FBMEASURE_BIN ?= build/bin/fbmeasure
VERSION ?= dev
LDFLAGS ?= -X github.com/NodePath81/fbforward/internal/version.Version=$(VERSION)

.PHONY: all build build-fbforward build-bwprobe build-fbmeasure clean test

all: build

build: build-fbforward build-bwprobe build-fbmeasure

build-fbforward:
	mkdir -p $(dir $(FBFORWARD_BIN))
	go build -ldflags "$(LDFLAGS)" -o $(FBFORWARD_BIN) ./cmd/fbforward

build-bwprobe:
	mkdir -p $(dir $(BWPROBE_BIN))
	go build -o $(BWPROBE_BIN) ./bwprobe/cmd

build-fbmeasure:
	mkdir -p $(dir $(FBMEASURE_BIN))
	go build -ldflags "$(LDFLAGS)" -o $(FBMEASURE_BIN) ./cmd/fbmeasure

test:
	go test ./...

clean:
	rm -rf build/bin
