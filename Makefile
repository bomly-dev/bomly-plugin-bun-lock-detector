.PHONY: build test package

BINARY ?= bomly-plugin-bun-lock-detector

build:
	go build -o bin/$(BINARY) .

test:
	go test ./...

package: build
	mkdir -p dist
	tar -czf dist/$(BINARY)_$$(go env GOOS)_$$(go env GOARCH).tar.gz bomly-plugin.json README.md -C bin $(BINARY)
