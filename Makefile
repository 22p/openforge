BINARY  := openforge
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
GOFLAGS := -trimpath

.PHONY: build run test vet fmt tidy docker clean

build:
	CGO_ENABLED=0 go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) .

run:
	go run . -config config.example.toml

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

tidy:
	go mod tidy

docker:
	docker build --build-arg VERSION=$(VERSION) -t openforge:$(VERSION) .

clean:
	rm -f $(BINARY)
