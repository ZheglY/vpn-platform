ARG GO_VERSION=1.26.5
ARG GO_BUILDER_DIGEST=sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2
ARG DISTROLESS_DIGEST=sha256:aef9602f8710ec12bde19d593fed1f76c708531bb7aba205110f1029786ead7b
ARG XRAY_VERSION=26.3.27
ARG XRAY_COMMIT=d2758a023cd7f4174a5a5fa4ff66e487d4342ba0
ARG XRAY_SOURCE_SHA256=14fa566ee0a801d3d51144c67018b449f5dcf462ddfacadd032da069787e61f9
ARG XRAY_CRYPTO_VERSION=v0.53.0
ARG XRAY_NET_VERSION=v0.56.0
ARG XRAY_TEXT_VERSION=v0.40.0
ARG XRAY_GRPC_VERSION=v1.82.1

FROM golang:${GO_VERSION}-alpine@${GO_BUILDER_DIGEST} AS xray-build
ARG XRAY_VERSION
ARG XRAY_COMMIT
ARG XRAY_SOURCE_SHA256
ARG XRAY_CRYPTO_VERSION
ARG XRAY_NET_VERSION
ARG XRAY_TEXT_VERSION
ARG XRAY_GRPC_VERSION
WORKDIR /src/xray
RUN wget -q -O /tmp/xray.tar.gz "https://github.com/XTLS/Xray-core/archive/${XRAY_COMMIT}.tar.gz" \
    && echo "${XRAY_SOURCE_SHA256}  /tmp/xray.tar.gz" | sha256sum -c - \
    && tar -xzf /tmp/xray.tar.gz --strip-components=1 \
    && rm /tmp/xray.tar.gz
RUN go mod edit -require="golang.org/x/crypto@${XRAY_CRYPTO_VERSION}" -require="golang.org/x/net@${XRAY_NET_VERSION}" -require="golang.org/x/text@${XRAY_TEXT_VERSION}" -require="google.golang.org/grpc@${XRAY_GRPC_VERSION}" \
    && go mod tidy \
    && go mod verify
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false \
    -gcflags="all=-l=4" \
    -ldflags="-X github.com/xtls/xray-core/core.build=stage7-security-rebuild-${XRAY_VERSION} -s -w -buildid=" \
    -o /out/xray ./main

FROM golang:${GO_VERSION}-alpine@${GO_BUILDER_DIGEST} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ internal/
COPY services/node-agent/ services/node-agent/
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X main.buildVersion=${VERSION} -X main.buildCommit=${COMMIT} -X main.buildDate=${DATE}" -o /out/node-agent ./services/node-agent/cmd/node-agent
RUN mkdir -p /out/node-state /out/xray-config

FROM gcr.io/distroless/static-debian12:nonroot@${DISTROLESS_DIGEST}
WORKDIR /
COPY --from=build /out/node-agent /node-agent
COPY --from=xray-build /out/xray /usr/local/bin/xray
COPY --from=build --chown=nonroot:nonroot /out/node-state /var/lib/node-agent
COPY --from=build --chown=nonroot:nonroot /out/xray-config /var/lib/xray
USER nonroot:nonroot
ENV HTTP_ADDR=:8443 XRAY_BINARY=/usr/local/bin/xray
EXPOSE 8443 443
HEALTHCHECK --interval=15s --timeout=4s --start-period=10s --retries=3 CMD ["/node-agent", "healthcheck"]
ENTRYPOINT ["/node-agent"]
