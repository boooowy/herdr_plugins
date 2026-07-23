.PHONY: build test vet tidy plugin-link clean

build:
	mkdir -p bin
	go build -o bin/agent-quota-meter .

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
