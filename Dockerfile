# syntax=docker/dockerfile:1
FROM node:24-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src/backend
# Use a Go module mirror reachable from mainland China; the default
# proxy.golang.org is blocked and makes every image build fail.
ENV GOPROXY=https://goproxy.cn,direct
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=frontend /src/frontend/dist/ ./internal/webui/dist/
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/zhan ./cmd/server

# Official CLI image includes Docker Compose and Buildx; use the host daemon.
FROM docker:29-cli
RUN apk add --no-cache ca-certificates git openssh-client tzdata
WORKDIR /app
COPY --from=backend /out/zhan /usr/local/bin/zhan
ENV ZHAN_LISTEN=0.0.0.0:12345
EXPOSE 12345
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
 CMD wget -q -O /dev/null http://127.0.0.1:12345/api/v1/health || exit 1
ENTRYPOINT ["zhan"]
