# syntax=docker/dockerfile:1
FROM golang:alpine AS build

WORKDIR /app

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/bin/kamal-watch ./cmd/kamal-watch

FROM alpine:3.20

RUN apk add --no-cache ca-certificates openssh-client tzdata

WORKDIR /app

COPY --from=build /app/bin/kamal-watch /app/kamal-watch

EXPOSE 8080

CMD ["/app/kamal-watch", "-config", "/app/config/kamal-watch.yml"]
