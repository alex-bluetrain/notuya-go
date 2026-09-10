BINARY := notuya
DIST := dist

.PHONY: build build-all test vet fmt clean

build:
	go build -o $(DIST)/$(BINARY) ./cmd/notuya

build-all: build-linux build-windows

build-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(DIST)/$(BINARY)-linux-amd64 ./cmd/notuya

build-windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o $(DIST)/$(BINARY)-windows-amd64.exe ./cmd/notuya

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

clean:
	rm -rf $(DIST)
