.PHONY: up down logs reset test

up:
	docker compose up --detach --build --wait

down:
	docker compose down

logs:
	docker compose logs --follow

reset:
	docker compose build transfer-service bank-a bank-b
	scripts/reset.sh

test:
	scripts/test.sh
