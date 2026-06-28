# Stage 1: Build
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod tidy && go mod download

COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /loop-mcp ./cmd/loop-mcp

# Stage 2: Runtime
FROM alpine:3.19

RUN apk add --no-cache ca-certificates && \
    adduser -D -u 1001 appuser
    

WORKDIR /app
COPY --from=builder /loop-mcp ./loop-mcp

USER appuser

EXPOSE 8080
ENTRYPOINT ["./loop-mcp"]
