# syntax=docker/dockerfile:1.7
FROM --platform=$BUILDPLATFORM golang:1.26.0-alpine3.23@sha256:d4c4845f5d60c6a974c6000ce58ae079328d03ab7f721a0734277e69905473e5 AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/smoke/fakeprovider ./internal/smoke/fakeprovider
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/fake-provider ./internal/smoke/fakeprovider

FROM scratch
COPY --from=build /out/fake-provider /fake-provider
USER 65532:65532
EXPOSE 8443
HEALTHCHECK --interval=2s --timeout=3s --start-period=2s --retries=30 CMD ["/fake-provider", "healthcheck"]
ENTRYPOINT ["/fake-provider"]
CMD ["serve"]
