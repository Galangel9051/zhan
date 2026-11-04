FROM golang:1.26-bookworm AS build
WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o /server ./cmd/server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates libsqlite3-0 && rm -rf /var/lib/apt/lists/* && useradd --system --uid 10001 app && mkdir -p /app/data && chown app:app /app/data
WORKDIR /app
COPY --from=build /server /app/server
ENV GIN_MODE=release HOST=0.0.0.0 PORT=8080 DB_PATH=/app/data/app.db
USER app
EXPOSE 8080
CMD ["/app/server"]
