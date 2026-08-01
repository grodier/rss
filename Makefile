.PHONY: run
run:
	go run cmd/www/main.go

.PHONY: build
build:
	go build -o bin/www cmd/www/main.go

.PHONY: test
test:
	go test ./...

.PHONY: clean
clean:
	rm -rf bin
