.PHONY: run
run:
	go run ./cmd/www

.PHONY: build
build:
	go build -o bin/www ./cmd/www

.PHONY: test
test:
	go test ./...

.PHONY: clean
clean:
	rm -rf bin
