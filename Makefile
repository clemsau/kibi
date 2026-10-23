.PHONY: api web build up down seed

api:
	cd backend && go run -ldflags='-linkmode=external' .

web:
	cd frontend && npm run dev

build:
	cd backend && go build -ldflags='-linkmode=external' ./...
	cd frontend && npm run build

up:
	docker compose up -d

down:
	docker compose down

seed:
	docker compose run --rm elasticsearch-seed
