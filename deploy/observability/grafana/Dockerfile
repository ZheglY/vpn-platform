ARG GO_VERSION=1.26.5
ARG GO_BUILDER_DIGEST=sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2
ARG GRAFANA_BASE_DIGEST=sha256:7cb8c64c4d57a57e734073f3cc94620adb24a0acb929bd80ba9f14017e3a975b
ARG GRAFANA_VERSION=13.1.1
ARG GRAFANA_COMMIT=a9cee6e1724a455676bb6c05eef7fc54aa4b19f4
ARG GRAFANA_SOURCE_SHA256=722f7374891d375f0900dbee6e638d8b692edeba5d2353dd1d9315a3372cf78c
ARG GRAFANA_GRPC_VERSION=v1.82.1
ARG GRAFANA_KIN_OPENAPI_VERSION=v0.144.0
ARG TEMPO_VERSION=v1.5.1-0.20260427112133-525d1bab07e0

FROM golang:${GO_VERSION}-alpine@${GO_BUILDER_DIGEST} AS build
ARG GRAFANA_VERSION
ARG GRAFANA_COMMIT
ARG GRAFANA_SOURCE_SHA256
ARG GRAFANA_GRPC_VERSION
ARG GRAFANA_KIN_OPENAPI_VERSION
ARG TEMPO_VERSION
WORKDIR /src/grafana
RUN wget -q -O /tmp/grafana.tar.gz "https://github.com/grafana/grafana/archive/${GRAFANA_COMMIT}.tar.gz" \
    && echo "${GRAFANA_SOURCE_SHA256}  /tmp/grafana.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/grafana.tar.gz --strip-components=1 \
    && rm /tmp/grafana.tar.gz
RUN go mod download "github.com/grafana/tempo@${TEMPO_VERSION}" \
    && tempo_dir="$(go env GOMODCACHE)/github.com/grafana/tempo@${TEMPO_VERSION}" \
    && mkdir -p /tmp/tempo-proto/pkg \
    && cp -R "${tempo_dir}/pkg/tempopb" /tmp/tempo-proto/pkg/tempopb \
    && printf 'module github.com/grafana/tempo\n\ngo 1.26.5\n' > /tmp/tempo-proto/go.mod \
    && go mod edit -replace=github.com/grafana/tempo=/tmp/tempo-proto \
    && go mod edit -require="google.golang.org/grpc@${GRAFANA_GRPC_VERSION}" \
    && go mod edit -require="github.com/getkin/kin-openapi@${GRAFANA_KIN_OPENAPI_VERSION}"
RUN go mod download \
    && go mod verify
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false \
    -ldflags="-s -w -buildid= -X main.version=${GRAFANA_VERSION} -X main.commit=${GRAFANA_COMMIT} -X main.buildBranch=stage8-security-rebuild -X main.buildstamp=1784565215" \
    -o /out/grafana ./pkg/cmd/grafana

FROM grafana/grafana:${GRAFANA_VERSION}@${GRAFANA_BASE_DIGEST}
USER root
COPY --from=build --chown=472:0 /out/grafana /usr/share/grafana/bin/grafana
RUN rm -rf \
    /usr/share/grafana/data/plugins-bundled/elasticsearch \
    /usr/share/grafana/data/plugins-bundled/zipkin
USER 472
