FROM golang:1.24-bookworm AS builder

ENV GOTOOLCHAIN=auto
ENV CGO_ENABLED=1

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o /out/harrow-server ./cmd/server && \
    go build -o /out/harrow-worker ./cmd/worker

FROM debian:bookworm-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /out/harrow-server /usr/local/bin/harrow-server
COPY --from=builder /out/harrow-worker /usr/local/bin/harrow-worker

EXPOSE 8080
CMD ["harrow-server"]
