# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go test ./...
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mock-issue-mcp ./cmd/mock-issue-mcp

FROM alpine:3.23
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/mock-issue-mcp /app/mock-issue-mcp
COPY data /app/data
RUN mkdir -p /app/reports && chown 65532:65532 /app/reports
USER 65532:65532
ENV LISTEN_ADDR=0.0.0.0:8090 DATA_PATH=/app/data/seed.json REPORTS_PATH=/app/reports
EXPOSE 8090
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 CMD wget -q -O /dev/null http://127.0.0.1:8090/healthz || exit 1
ENTRYPOINT ["/app/mock-issue-mcp"]
