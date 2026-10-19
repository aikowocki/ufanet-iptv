FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/stream ./cmd/stream

# Учебный стрим-сервер: ffmpeg с drawtext (freetype) и шрифт с кириллицей для названия канала.
# Исходный ролик монтируется томом, см. docker-compose.yml.
FROM alpine:3.22 AS stream
RUN apk add --no-cache ffmpeg font-dejavu \
 && adduser -D -u 10001 app
USER app
COPY --from=build /out/stream /usr/local/bin/stream
EXPOSE 8081
ENTRYPOINT ["stream"]

# API. Последняя стадия, поэтому `docker build` без --target собирает именно её.
FROM alpine:3.22 AS server
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/server /usr/local/bin/server
EXPOSE 8080
ENTRYPOINT ["server"]
