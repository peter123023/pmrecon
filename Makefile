BIN := bin/pmrecon

.PHONY: all build test vet race clean

all: build

build:
	@mkdir -p bin
	go build -o $(BIN) ./cmd/pmrecon

test:
	go test ./...

vet:
	go vet ./...

race:
	go test -race ./...

clean:
	rm -rf bin
