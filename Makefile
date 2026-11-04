.PHONY: build check dev-backend dev-frontend
build:
	./scripts/build.sh
check:
	./scripts/check.sh
dev-backend:
	cd backend && go run ./cmd/server
dev-frontend:
	cd frontend && npm run dev
