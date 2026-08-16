# syntax=docker/dockerfile:1.7
FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine3.23@sha256:5978cc992ad5ef96a7469713c8af849c1433824761ce3be2c56381403cd8d9a3 AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.1.0
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -trimpath -buildvcs=true -ldflags="-s -w -X main.version=$VERSION" \
    -o /out/harden-llm-gateway ./cmd/harden-llm-gateway

FROM scratch
ARG VERSION=0.1.0
LABEL org.opencontainers.image.title="harden-llm-gateway" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/harden-llm-gateway /harden-llm-gateway
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=12 \
    CMD ["/harden-llm-gateway", "healthcheck"]
ENTRYPOINT ["/harden-llm-gateway"]
