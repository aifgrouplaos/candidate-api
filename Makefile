.PHONY: run build docker-up docker-down test tidy

run:
	go run cmd/api/main.go

build:
	go build -o bin/candidate-api cmd/api/main.go

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

test:
	go test ./...

tidy:
	go mod tidy
