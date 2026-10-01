GO ?= go
PKG := ./...
BINARY := rdma_exporter

.PHONY: all build test lint fmt clean grafana-com-export

all: build

build:
	$(GO) build -o $(BINARY) .

test:
	$(GO) test $(PKG)

lint:
	$(GO) vet $(PKG)

fmt:
	gofmt -w $(shell find . -type f -name '*.go')

grafana-com-export:
	$(GO) run ./dashboards/cmd/grafana-com-export .

clean:
	rm -f $(BINARY)
