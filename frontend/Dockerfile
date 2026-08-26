ARG ELIXIR_IMAGE=hexpm/elixir:1.20.2-erlang-28.4.3-alpine-3.23.5@sha256:47cf4ef1fe9aeeec729da32eaa58b3d9ddbbe138c956f1f69d3794d536629105
ARG RUNTIME_IMAGE=alpine:3.23.5@sha256:fd791d74b68913cbb027c6546007b3f0d3bc45125f797758156952bc2d6daf40

FROM ${ELIXIR_IMAGE} AS build

RUN apk add --no-cache \
      build-base=0.5-r3 \
      git=2.52.0-r0

ENV MIX_ENV=prod
WORKDIR /build

RUN mix local.hex --force && mix local.rebar --force

COPY mix.exs mix.lock ./
RUN mix deps.get --only prod && mix deps.compile

COPY config config
COPY assets assets
COPY lib lib
COPY priv priv

RUN mix assets.deploy && mix release --path /release

FROM ${RUNTIME_IMAGE} AS runtime

ARG VERSION=dev

RUN apk add --no-cache \
      ca-certificates=20260611-r0 \
      libgcc=15.2.0-r2 \
      libstdc++=15.2.0-r2 \
      ncurses-libs=6.5_p20251123-r0 \
      openssl=3.5.8-r0 \
    && addgroup -S -g 10001 harden_llm \
    && adduser -S -D -H -u 10001 -G harden_llm harden_llm \
    && mkdir -p /app /var/log/harden-llm-web \
    && chown -R harden_llm:harden_llm /app /var/log/harden-llm-web

WORKDIR /app
COPY --from=build --chown=harden_llm:harden_llm /release/ ./

LABEL org.opencontainers.image.title="harden-llm-web" \
      org.opencontainers.image.version="${VERSION}"

ENV HOME=/app \
    LANG=C.UTF-8 \
    PHX_SERVER=true \
    PORT=4000

USER 10001:10001
EXPOSE 4000

ENTRYPOINT ["/app/bin/harden_llm"]
CMD ["start"]
