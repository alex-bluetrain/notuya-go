.PHONY: build test vet fmt check

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

check: vet test

fmt:
	gofmt -l .
