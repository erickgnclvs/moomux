.PHONY: build test test-e2e lint install run clean check-deps

BIN := moomux
PREFIX ?= $(HOME)/.local

REQUIRED_BINS := tmux git

check-deps:
	@missing=""; \
	for bin in $(REQUIRED_BINS); do \
		if ! command -v $$bin >/dev/null 2>&1; then \
			missing="$$missing $$bin"; \
		fi; \
	done; \
	if [ -n "$$missing" ]; then \
		echo "Error: missing required dependencies:$$missing"; \
		echo ""; \
		echo "Install with:"; \
		echo "  macOS:  brew install$$missing"; \
		echo "  Ubuntu: sudo apt install$$missing"; \
		echo "  Fedora: sudo dnf install$$missing"; \
		exit 1; \
	fi

build:
	go build -o $(BIN) .

test:
	go test ./... -race -shuffle=on -count=1

# Exercises the real App against real tmux/git binaries: creates actual
# worktrees and tmux sessions under a temp dir, then tears them down.
test-e2e: check-deps
	go test -tags e2e ./e2e/... -race -shuffle=on -count=1

# Same gates as the `lint` job in .github/workflows/test.yml. The tool
# versions are pinned there too; keep them in step.
lint:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then echo "not gofmt'd:"; echo "$$unformatted"; exit 1; fi
	go run honnef.co/go/tools/cmd/staticcheck@v0.8.1 ./...
	go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
	./scripts/next_version_test.sh

install: check-deps build
	mkdir -p "$(PREFIX)/bin"
	cp $(BIN) "$(PREFIX)/bin/$(BIN)"

run: check-deps build
	./$(BIN)

clean:
	rm -f $(BIN) "$(PREFIX)/bin/$(BIN)"
