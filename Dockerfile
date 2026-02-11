# --- Build stage ---
FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /server ./cmd/server

# --- Runtime stage ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /server /server
COPY web/ /web/

WORKDIR /
ENV TZ=Europe/Kyiv
EXPOSE 8080

CMD ["/server"]
