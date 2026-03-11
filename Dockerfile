ARG GO_VERSION=1.26.1

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src

ARG TARGETOS
ARG TARGETARCH

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY config.local.json ./config.local.json

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build \
    -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${BUILD_DATE}" \
    -o /out/thin-llama ./cmd/thin-llama

FROM ghcr.io/ggml-org/llama.cpp:server

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl tini && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/thin-llama /usr/local/bin/thin-llama
COPY --from=build /src/config.local.json /app/config.local.json

RUN ln -sf /app/llama-server /usr/local/bin/llama-server && mkdir -p /models /state

WORKDIR /app
VOLUME ["/models", "/state"]
EXPOSE 8080
ENTRYPOINT ["/usr/bin/tini", "--", "/usr/local/bin/thin-llama", "serve", "--config", "/app/config.local.json"]
