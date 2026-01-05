UI_DIR := ui
UI_VITE := $(UI_DIR)/node_modules/.bin/vite
BIN_OUT ?= fbforward

.PHONY: all ui-build build clean

all: build

ui-build:
	@if [ -x "$(UI_VITE)" ]; then \
		npm --prefix $(UI_DIR) run build; \
	else \
		echo "vite not installed; skipping ui build and using existing ui-dist"; \
	fi

build: ui-build
	go build -o $(BIN_OUT) .

clean:
	rm -rf ui-dist
