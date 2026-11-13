BINARY  := llmmem
GO      ?= go
BIN_DIR := bin

.PHONY: all build test vet fmt install uninstall skill-install run-mcp clean

all: build

build:
	$(GO) build -trimpath -o $(BIN_DIR)/$(BINARY) ./cmd/llmmem

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

install:
	$(GO) install ./cmd/llmmem

# Run the MCP server over HTTP on the default address.
run-mcp:
	$(GO) run ./cmd/llmmem mcp

uninstall:
	rm -f $(shell $(GO) env GOPATH)/bin/$(BINARY)

# Make the skill available to every OpenCode project for this user.
skill-install:
	mkdir -p $(HOME)/.config/opencode/skills/llm-memory
	cp .opencode/skills/llm-memory/SKILL.md $(HOME)/.config/opencode/skills/llm-memory/SKILL.md

clean:
	rm -rf $(BIN_DIR)
