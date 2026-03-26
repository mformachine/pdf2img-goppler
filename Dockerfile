FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod ./
COPY main.go ./

RUN go build -ldflags="-s -w" -o poppler-api .

FROM alpine:3.20

RUN apk add --no-cache poppler-utils ca-certificates dumb-init

WORKDIR /app

COPY --from=builder /app/poppler-api /app/poppler-api

RUN mkdir -p /app/media && adduser -D -u 10001 appuser && chown -R appuser:appuser /app

USER appuser

EXPOSE 5000

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -q -O - http://127.0.0.1:5000/healthz >/dev/null 2>&1 || exit 1

ENTRYPOINT ["dumb-init", "--"]
CMD ["/app/poppler-api"]
