.PHONY: build test install clean

VERSION ?= 0.1.0
LDFLAGS := -X github.com/timjonez/gate/internal/gatecli.Version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o bin/gate ./cmd/gate

test:
	go test ./...

install:
	go install -ldflags "$(LDFLAGS)" ./cmd/gate

clean:
	rm -rf bin
