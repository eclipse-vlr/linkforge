# Сборка API. SQL-миграции читаются с диска при запуске.
FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/linkforge ./cmd/api

FROM alpine:3.24
RUN apk add --no-cache ca-certificates && addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --chown=app:app --from=build /out/linkforge ./linkforge
COPY --chown=app:app --from=build /src/migrations ./migrations
USER app
EXPOSE 8080

# Railway передаёт PORT; приложение читает HTTP_ADDR.
CMD ["sh", "-c", "export HTTP_ADDR=\"${HTTP_ADDR:-0.0.0.0:${PORT:-8080}}\"; exec ./linkforge"]
