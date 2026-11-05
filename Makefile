PROJECT_ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

.PHONY: build check dev-backend dev-frontend
build:
	./scripts/build.sh
check:
	./scripts/check.sh
dev-backend:
	cd "$(PROJECT_ROOT)/backend" && go run ./cmd/server
dev-frontend:
	cd frontend && npm run dev

.PHONY: docker-up docker-down docker-logs
docker-up:
	./scripts/docker-up.sh
docker-down:
	./scripts/docker-up.sh down
docker-logs:
	./scripts/docker-up.sh logs -f app
