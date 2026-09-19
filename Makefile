.PHONY: run build tidy test docker-up docker-down

run:
	go run .

build:
	go build -o expense-tracker .

tidy:
	go mod tidy

test:
	go test ./...

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down
