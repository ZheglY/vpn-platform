ARG GO_VERSION=1.26.5
ARG GO_BUILDER_DIGEST=sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2
ARG RUNTIME_DIGEST=sha256:aef9602f8710ec12bde19d593fed1f76c708531bb7aba205110f1029786ead7b
ARG LOKI_VERSION=3.7.2
ARG LOKI_COMMIT=7486c4a755a089363f07403dfa1c5fe727f25f4a
ARG LOKI_SOURCE_SHA256=196c5d8214f5bdc8795fedf09a60f7418f4d098cb87edb4632c613abe21ed53d
ARG GRPC_VERSION=v1.82.1
ARG THRIFT_VERSION=v0.23.0
ARG PROMETHEUS_VERSION=v0.311.3
ARG X_CRYPTO_VERSION=v0.52.0
ARG X_NET_VERSION=v0.55.0

FROM golang:${GO_VERSION}-alpine@${GO_BUILDER_DIGEST} AS build
ARG LOKI_VERSION
ARG LOKI_COMMIT
ARG LOKI_SOURCE_SHA256
ARG GRPC_VERSION
ARG THRIFT_VERSION
ARG PROMETHEUS_VERSION
ARG X_CRYPTO_VERSION
ARG X_NET_VERSION
WORKDIR /src/loki
RUN wget -q -O /tmp/loki.tar.gz "https://github.com/grafana/loki/archive/${LOKI_COMMIT}.tar.gz" \
    && echo "${LOKI_SOURCE_SHA256}  /tmp/loki.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/loki.tar.gz --strip-components=1 \
    && rm /tmp/loki.tar.gz
COPY deploy/observability/loki/prometheus-0.311.3-compat.patch /tmp/prometheus-0.311.3-compat.patch
RUN apk add --no-cache patch \
    && patch -p1 < /tmp/prometheus-0.311.3-compat.patch
RUN go mod edit \
        -require="github.com/apache/thrift@${THRIFT_VERSION}" \
        -require="github.com/prometheus/prometheus@${PROMETHEUS_VERSION}" \
        -require="golang.org/x/crypto@${X_CRYPTO_VERSION}" \
        -require="golang.org/x/net@${X_NET_VERSION}" \
        -require="google.golang.org/grpc@${GRPC_VERSION}"
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download \
    && go mod verify
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=mod -trimpath \
    -tags=netgo \
    -ldflags="-s -w -buildid= -X github.com/grafana/loki/v3/pkg/util/build.Branch=stage8-security-rebuild -X github.com/grafana/loki/v3/pkg/util/build.Version=${LOKI_VERSION} -X github.com/grafana/loki/v3/pkg/util/build.Revision=${LOKI_COMMIT}" \
    -o /out/loki ./cmd/loki
COPY tools/httphealth/cmd/httphealth/main.go /src/httphealth/main.go
WORKDIR /src/httphealth
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -buildid=" \
    -o /out/httphealth /src/httphealth/main.go \
    && mkdir -p /out/licenses/loki /out/loki-data \
    && cp /src/loki/LICENSE /src/loki/LICENSING.md /out/licenses/loki/

FROM gcr.io/distroless/static-debian12:nonroot@${RUNTIME_DIGEST}
ARG LOKI_VERSION
ARG LOKI_COMMIT
COPY --from=build /out/loki /usr/bin/loki
COPY --from=build /out/httphealth /bin/httphealth
COPY --from=build /out/licenses /licenses
COPY --from=build --chown=10001:10001 /out/loki-data /var/loki
USER 10001:10001
ENTRYPOINT ["/usr/bin/loki"]
CMD ["-config.file=/etc/loki/local-config.yaml"]
LABEL org.opencontainers.image.version="${LOKI_VERSION}" \
      org.opencontainers.image.revision="${LOKI_COMMIT}" \
      org.opencontainers.image.source="https://github.com/grafana/loki" \
      io.vpn-platform.security-rebuild.go="1.26.5"
