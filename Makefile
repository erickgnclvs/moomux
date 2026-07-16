.PHONY: build test test-e2e install run clean check-deps

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
	go test ./... -race -count=1

# Exercises the real App against real tmux/git binaries: creates actual
# worktrees and tmux sessions under a temp dir, then tears them down.
test-e2e: check-deps
	go test -tags e2e ./e2e/... -count=1

install: check-deps build
	mkdir -p $(PREFIX)/bin
	cp $(BIN) $(PREFIX)/bin/$(BIN)

run: check-deps build
	./$(BIN)

clean:
	rm -f $(BIN)
