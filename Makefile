.PHONY: build test install run clean

BIN := curral
PREFIX ?= $(HOME)/.local

build:
	go build -o $(BIN) .

test:
	go test ./... -race -count=1

install: build
	mkdir -p $(PREFIX)/bin
	cp $(BIN) $(PREFIX)/bin/$(BIN)

run: build
	./$(BIN)

clean:
	rm -f $(BIN)
