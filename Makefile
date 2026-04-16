build:
	go build -o bin/fry ./cmd/fry

test:
	go test -race ./...

lint:
	golangci-lint run

clean:
	rm -rf bin/

PREFIX ?= $(HOME)/.local

install: build
	mkdir -p $(PREFIX)/bin
	cp bin/fry $(PREFIX)/bin/fry
	@if [ "$$(uname -s)" = "Darwin" ]; then \
		if ! command -v codesign >/dev/null 2>&1; then \
			echo "make install: codesign is required on macOS to sign $(PREFIX)/bin/fry after install" >&2; \
			exit 1; \
		fi; \
		codesign --force --sign - $(PREFIX)/bin/fry; \
	fi
