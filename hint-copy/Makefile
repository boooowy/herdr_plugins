.PHONY: build test vet tidy plugin-link clean

build:
	mkdir -p bin
	go build -o bin/hint-copy .

test:
	go test -race ./...

vet:
	go vet ./...

tidy:
	go mod tidy

plugin-link: build
	herdr plugin link $(CURDIR)

clean:
	rm -rf bin
