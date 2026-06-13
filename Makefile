.PHONY: build run test docker-up docker-down clean lint

build:
	go build -o bin/api-gateway ./services/api-gateway/cmd

run:
	go run ./services/api-gateway/cmd

test:
	go test ./...

lint:
	go vet ./...

docker-up:
	docker-compose -f infra/docker-compose.yml up -d

docker-down:
	docker-compose -f infra/docker-compose.yml down

docker-down-volumes:
	docker-compose -f infra/docker-compose.yml down -v

clean:
	rm -rf bin/
