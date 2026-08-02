ARG GO_VERSION=1.26.5
ARG GO_BUILDER_DIGEST=sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2
ARG RUNTIME_DIGEST=sha256:aef9602f8710ec12bde19d593fed1f76c708531bb7aba205110f1029786ead7b
ARG TEMPO_VERSION=2.10.5
ARG TEMPO_TAG_OBJECT=a1dd9bfb94ee481e11da1694064c3f0feae3ab6b
ARG TEMPO_COMMIT=991ce39eb956e9ed771fcffe05eff42d33de27ba
ARG TEMPO_SOURCE_SHA256=d8d1c1c7949343263621fa5d6b98030486841d1fb64622bbbbcb7ac21b593540
ARG GRPC_VERSION=v1.82.1
ARG THRIFT_VERSION=v0.23.0
ARG PROMETHEUS_VERSION=v0.305.5
ARG PROMETHEUS_OTLPTRANSLATOR_VERSION=v0.0.0-20250527173959-2573485683d5
ARG OTEL_PROMETHEUS_EXPORTER_VERSION=v0.59.0
ARG X_CRYPTO_VERSION=v0.52.0
ARG X_NET_VERSION=v0.55.0
ARG X_TEXT_VERSION=v0.40.0

FROM golang:${GO_VERSION}-alpine@${GO_BUILDER_DIGEST} AS build
ARG TEMPO_VERSION
ARG TEMPO_COMMIT
ARG TEMPO_SOURCE_SHA256
ARG GRPC_VERSION
ARG THRIFT_VERSION
ARG PROMETHEUS_VERSION
ARG PROMETHEUS_OTLPTRANSLATOR_VERSION
ARG OTEL_PROMETHEUS_EXPORTER_VERSION
ARG X_CRYPTO_VERSION
ARG X_NET_VERSION
ARG X_TEXT_VERSION
WORKDIR /src/tempo
RUN wget -q -O /tmp/tempo.tar.gz "https://github.com/grafana/tempo/archive/${TEMPO_COMMIT}.tar.gz" \
    && echo "${TEMPO_SOURCE_SHA256}  /tmp/tempo.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/tempo.tar.gz --strip-components=1 \
    && rm /tmp/tempo.tar.gz
COPY deploy/observability/tempo/prometheus-0.305-compat.patch /tmp/prometheus-0.305-compat.patch
RUN apk add --no-cache patch \
    && patch -p1 < /tmp/prometheus-0.305-compat.patch
RUN go mod edit \
        -require="github.com/apache/thrift@${THRIFT_VERSION}" \
        -require="github.com/prometheus/otlptranslator@${PROMETHEUS_OTLPTRANSLATOR_VERSION}" \
        -require="github.com/prometheus/prometheus@${PROMETHEUS_VERSION}" \
        -require="go.opentelemetry.io/otel/exporters/prometheus@${OTEL_PROMETHEUS_EXPORTER_VERSION}" \
        -require="golang.org/x/crypto@${X_CRYPTO_VERSION}" \
        -require="golang.org/x/net@${X_NET_VERSION}" \
        -require="golang.org/x/text@${X_TEXT_VERSION}" \
        -require="google.golang.org/grpc@${GRPC_VERSION}" \
    && go mod edit \
        -replace="github.com/prometheus/otlptranslator=github.com/prometheus/otlptranslator@${PROMETHEUS_OTLPTRANSLATOR_VERSION}" \
        -replace="go.opentelemetry.io/otel/exporters/prometheus=go.opentelemetry.io/otel/exporters/prometheus@${OTEL_PROMETHEUS_EXPORTER_VERSION}"
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download \
    && go mod verify \
    && grep -F 'ClientSecret config_util.Secret `yaml:"client_secret,omitempty"`' \
        "$(go env GOMODCACHE)/github.com/prometheus/prometheus@${PROMETHEUS_VERSION}/storage/remote/azuread/azuread.go"
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v2 GOMAXPROCS=4 go build -p=4 -mod=mod -trimpath \
    -tags=netgo \
    -ldflags="-s -w -buildid= -X main.Branch=stage8-security-rebuild -X main.Revision=${TEMPO_COMMIT} -X main.Version=${TEMPO_VERSION}" \
    -o /out/tempo ./cmd/tempo
COPY tools/httphealth/cmd/httphealth/main.go /src/httphealth/main.go
WORKDIR /src/httphealth
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -buildid=" \
    -o /out/httphealth /src/httphealth/main.go \
    && mkdir -p /out/licenses/tempo /out/tempo-data \
    && cp /src/tempo/LICENSE /src/tempo/LICENSING.md /out/licenses/tempo/

FROM gcr.io/distroless/static-debian12:nonroot@${RUNTIME_DIGEST}
ARG TEMPO_VERSION
ARG TEMPO_TAG_OBJECT
ARG TEMPO_COMMIT
COPY --from=build /out/tempo /tempo
COPY --from=build /out/httphealth /bin/httphealth
COPY --from=build /out/licenses /licenses
COPY --from=build --chown=10001:10001 /out/tempo-data /var/tempo
USER 10001:10001
ENTRYPOINT ["/tempo"]
LABEL org.opencontainers.image.version="${TEMPO_VERSION}" \
      org.opencontainers.image.revision="${TEMPO_COMMIT}" \
      org.opencontainers.image.source="https://github.com/grafana/tempo" \
      io.vpn-platform.upstream-tag-object="${TEMPO_TAG_OBJECT}" \
      io.vpn-platform.security-rebuild.go="1.26.5"
