ARG GO_VERSION=1.26.5
ARG GO_BUILDER_DIGEST=sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2
ARG DISTROLESS_DIGEST=sha256:aef9602f8710ec12bde19d593fed1f76c708531bb7aba205110f1029786ead7b
ARG OTEL_COLLECTOR_VERSION=0.157.0
ARG OTEL_COLLECTOR_TAG_OBJECT=ebe705f0d7c3b99129ba65e405eafbf0341a79b0
ARG OTEL_COLLECTOR_COMMIT=4908404e59e544297b989a6961ee918b6f84b606

FROM golang:${GO_VERSION}-alpine@${GO_BUILDER_DIGEST} AS build
ARG OTEL_COLLECTOR_VERSION
WORKDIR /src
RUN apk add --no-cache ca-certificates
RUN go install "go.opentelemetry.io/collector/cmd/builder@v${OTEL_COLLECTOR_VERSION}"
COPY deploy/observability/otel-collector/builder-config.yaml /src/builder-config.yaml
RUN /go/bin/builder --config=/src/builder-config.yaml \
    && cd /src/dist \
    && go mod verify
COPY tools/httphealth/cmd/httphealth/main.go /src/httphealth/main.go
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -buildid=" \
    -o /out/httphealth /src/httphealth/main.go
RUN mkdir -p /out/licenses/collector /out/licenses/collector-contrib \
    && cp "$(go env GOMODCACHE)/go.opentelemetry.io/collector@v${OTEL_COLLECTOR_VERSION}/LICENSE" /out/licenses/collector/LICENSE \
    && cp "$(go env GOMODCACHE)/github.com/open-telemetry/opentelemetry-collector-contrib/processor/transformprocessor@v${OTEL_COLLECTOR_VERSION}/LICENSE" /out/licenses/collector-contrib/LICENSE

FROM gcr.io/distroless/static-debian12:nonroot@${DISTROLESS_DIGEST}
ARG OTEL_COLLECTOR_VERSION
ARG OTEL_COLLECTOR_TAG_OBJECT
ARG OTEL_COLLECTOR_COMMIT
COPY --from=build /src/dist/otelcol-vpn /bin/otelcol-vpn
COPY --from=build /out/httphealth /bin/httphealth
COPY --from=build /out/licenses /licenses
LABEL org.opencontainers.image.version="${OTEL_COLLECTOR_VERSION}" \
      org.opencontainers.image.revision="${OTEL_COLLECTOR_COMMIT}" \
      org.opencontainers.image.source="https://github.com/open-telemetry/opentelemetry-collector" \
      io.vpn-platform.upstream-tag-object="${OTEL_COLLECTOR_TAG_OBJECT}"
USER nonroot:nonroot
EXPOSE 4318 13133
ENTRYPOINT ["/bin/otelcol-vpn"]
