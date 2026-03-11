ARG GO_VERSION=1.26.1

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src

ARG TARGETOS
ARG TARGETARCH

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /out/thin-llama ./cmd/thin-llama

FROM ghcr.io/ggml-org/llama.cpp:server AS llama

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates tini && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/thin-llama /usr/local/bin/thin-llama
COPY --from=llama /app/llama-server /usr/local/bin/llama-server

RUN mkdir -p /models /state /config

WORKDIR /app
EXPOSE 8080
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/thin-llama", "serve", "--config", "/config/config.json"]
