UI_DIR := web
UI_VITE := $(UI_DIR)/node_modules/.bin/vite
BIN_OUT ?= build/bin/fbforward
VERSION ?= dev
LDFLAGS ?= -X github.com/NodePath81/fbforward/internal/version.Version=$(VERSION)

.PHONY: all ui-build build clean

all: build

ui-build:
	@if [ -x "$(UI_VITE)" ]; then \
		npm --prefix $(UI_DIR) run build; \
	else \
		echo "vite not installed; skipping ui build and using existing web/dist"; \
	fi

build: ui-build
	mkdir -p $(dir $(BIN_OUT))
	go build -ldflags "$(LDFLAGS)" -o $(BIN_OUT) ./cmd/fbforward

clean:
	rm -rf web/dist
