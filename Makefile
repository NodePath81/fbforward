UI_DIR := web
UI_VITE := $(UI_DIR)/node_modules/.bin/vite
FBFORWARD_BIN ?= build/bin/fbforward
BWPROBE_BIN ?= build/bin/bwprobe
VERSION ?= dev
LDFLAGS ?= -X github.com/NodePath81/fbforward/internal/version.Version=$(VERSION)

.PHONY: all ui-build build build-fbforward build-bwprobe clean test

all: build

ui-build:
	@if [ -x "$(UI_VITE)" ]; then \
		npm --prefix $(UI_DIR) run build; \
	else \
		echo "vite not installed; skipping ui build and using existing web/dist"; \
	fi

build: build-fbforward build-bwprobe

build-fbforward: ui-build
	mkdir -p $(dir $(FBFORWARD_BIN))
	go build -ldflags "$(LDFLAGS)" -o $(FBFORWARD_BIN) ./cmd/fbforward

build-bwprobe:
	mkdir -p $(dir $(BWPROBE_BIN))
	go build -o $(BWPROBE_BIN) ./bwprobe/cmd/bwprobe

test:
	go test ./...

clean:
	rm -rf web/dist build/bin
