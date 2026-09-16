BINARY := notuya
DAEMON := notuyad
DIST := dist

.PHONY: build build-all build-linux build-windows test vet fmt clean

build:
	go build -o $(DIST)/$(BINARY) ./cmd/notuya
	go build -o $(DIST)/$(DAEMON) ./cmd/notuyad

build-all: build-linux build-windows

build-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(DIST)/$(BINARY)-linux-amd64 ./cmd/notuya
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(DIST)/$(DAEMON)-linux-amd64 ./cmd/notuyad

build-windows:
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o $(DIST)/$(BINARY)-windows-amd64.exe ./cmd/notuya
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o $(DIST)/$(DAEMON)-windows-amd64.exe ./cmd/notuyad

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l .

clean:
	rm -rf $(DIST)
