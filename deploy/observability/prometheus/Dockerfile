ARG GO_VERSION=1.26.5
ARG GO_BUILDER_DIGEST=sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2
ARG DISTROLESS_DIGEST=sha256:aef9602f8710ec12bde19d593fed1f76c708531bb7aba205110f1029786ead7b
ARG PROMETHEUS_VERSION=3.13.1
ARG PROMETHEUS_TAG_OBJECT=3c2a2ff7aec85b531d74954cc99da75844dc106a
ARG PROMETHEUS_COMMIT=73ff57ce2b8161059ac7fe5188f03f1c3d22b29a
ARG PROMETHEUS_SOURCE_SHA256=322276709c16cb3c22a4d01d244beb91f0380a257699358d2a20ac7086d7256c
ARG PROMETHEUS_WEB_UI_SHA256=2194bfbb5d36457df2b8f480037ce89786ee32f03b5ee6ad5597989f14deafb0
ARG PROMETHEUS_GRPC_VERSION=v1.82.1

FROM golang:${GO_VERSION}-alpine@${GO_BUILDER_DIGEST} AS build
ARG PROMETHEUS_VERSION
ARG PROMETHEUS_COMMIT
ARG PROMETHEUS_SOURCE_SHA256
ARG PROMETHEUS_WEB_UI_SHA256
ARG PROMETHEUS_GRPC_VERSION
WORKDIR /src/prometheus
RUN apk add --no-cache bash coreutils findutils gzip
RUN wget -q -O /tmp/prometheus.tar.gz "https://github.com/prometheus/prometheus/archive/${PROMETHEUS_COMMIT}.tar.gz" \
    && echo "${PROMETHEUS_SOURCE_SHA256}  /tmp/prometheus.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/prometheus.tar.gz --strip-components=1 \
    && rm /tmp/prometheus.tar.gz
RUN wget -q -O /tmp/prometheus-web-ui.tar.gz \
        "https://github.com/prometheus/prometheus/releases/download/v${PROMETHEUS_VERSION}/prometheus-web-ui-${PROMETHEUS_VERSION}.tar.gz" \
    && echo "${PROMETHEUS_WEB_UI_SHA256}  /tmp/prometheus-web-ui.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/prometheus-web-ui.tar.gz -C web/ui \
    && rm /tmp/prometheus-web-ui.tar.gz \
    && PREBUILT_ASSETS_STATIC_DIR=web/ui/static scripts/compress_assets.sh
RUN go mod edit -require="google.golang.org/grpc@${PROMETHEUS_GRPC_VERSION}" \
    && go mod tidy \
    && go mod verify
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -tags="netgo,builtinassets" \
    -ldflags="-s -w -buildid= -X github.com/prometheus/common/version.Version=${PROMETHEUS_VERSION} -X github.com/prometheus/common/version.Revision=${PROMETHEUS_COMMIT} -X github.com/prometheus/common/version.Branch=stage8-security-rebuild -X github.com/prometheus/common/version.BuildUser=vpn-platform@local -X github.com/prometheus/common/version.BuildDate=20260710-081152" \
    -o /out/prometheus ./cmd/prometheus
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false -tags="netgo,builtinassets" \
    -ldflags="-s -w -buildid= -X github.com/prometheus/common/version.Version=${PROMETHEUS_VERSION} -X github.com/prometheus/common/version.Revision=${PROMETHEUS_COMMIT} -X github.com/prometheus/common/version.Branch=stage8-security-rebuild -X github.com/prometheus/common/version.BuildUser=vpn-platform@local -X github.com/prometheus/common/version.BuildDate=20260710-081152" \
    -o /out/promtool ./cmd/promtool
RUN mkdir -p /out/prometheus-data

FROM gcr.io/distroless/static-debian12:nonroot@${DISTROLESS_DIGEST}
ARG PROMETHEUS_VERSION
ARG PROMETHEUS_TAG_OBJECT
ARG PROMETHEUS_COMMIT
WORKDIR /prometheus
COPY --from=build /out/prometheus /bin/prometheus
COPY --from=build /out/promtool /bin/promtool
COPY --from=build --chown=nonroot:nonroot /out/prometheus-data /prometheus
COPY --from=build /src/prometheus/LICENSE /licenses/prometheus/LICENSE
COPY --from=build /src/prometheus/NOTICE /licenses/prometheus/NOTICE
LABEL org.opencontainers.image.version="${PROMETHEUS_VERSION}" \
      org.opencontainers.image.revision="${PROMETHEUS_COMMIT}" \
      org.opencontainers.image.source="https://github.com/prometheus/prometheus" \
      io.vpn-platform.upstream-tag-object="${PROMETHEUS_TAG_OBJECT}"
USER nonroot:nonroot
EXPOSE 9090
ENTRYPOINT ["/bin/prometheus"]
