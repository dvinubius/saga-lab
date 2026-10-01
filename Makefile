.PHONY: up down logs test

up:
	docker compose up --detach --build --wait

down:
	docker compose down

logs:
	docker compose logs --follow

test:
	scripts/test.sh
