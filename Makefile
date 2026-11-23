.PHONY: build test check cross-build

build:
	mkdir -p build
	go build -trimpath -o build/proxmanager ./cmd/proxmanager

test: build
	go test -race ./...
	python3 tests/node_status_integration.py build/proxmanager

check: test
	go vet ./...

cross-build:
	mkdir -p build
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o build/proxmanager-linux-amd64 ./cmd/proxmanager
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o build/proxmanager-linux-arm64 ./cmd/proxmanager
