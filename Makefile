.PHONY: run build

run:
	go run ./cmd/api

build:
	go build -o bin/api ./cmd/api
