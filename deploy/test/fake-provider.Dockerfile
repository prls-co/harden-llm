# syntax=docker/dockerfile:1.7
FROM --platform=$BUILDPLATFORM golang:1.26.11-alpine3.23@sha256:db99415e2625ce0ee03eed09edf7f32ae8c58dc399f28cc602a87fe7238119e4 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
COPY internal/smoke/fakeprovider ./internal/smoke/fakeprovider
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/fake-provider ./internal/smoke/fakeprovider

FROM scratch
COPY --from=build /out/fake-provider /fake-provider
USER 65532:65532
EXPOSE 8443
HEALTHCHECK --interval=2s --timeout=3s --start-period=2s --retries=30 CMD ["/fake-provider", "healthcheck"]
ENTRYPOINT ["/fake-provider"]
CMD ["serve"]
