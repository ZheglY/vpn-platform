ARG GO_VERSION=1.26.5
ARG GO_BUILDER_DIGEST=sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2
ARG POSTGRES_DIGEST=sha256:9a8afca54e7861fd90fab5fdf4c42477a6b1cb7d293595148e674e0a3181de15

FROM golang:${GO_VERSION}-alpine@${GO_BUILDER_DIGEST} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY tools/backupctl/ tools/backupctl/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/backupctl ./tools/backupctl && \
    cp "$(go env GOPATH)/pkg/mod/filippo.io/age@v1.3.1/LICENSE" /out/AGE-LICENSE

FROM postgres:18-alpine@${POSTGRES_DIGEST}
RUN apk upgrade --no-cache c-ares && \
    rm -f /usr/local/bin/gosu
COPY --from=build /out/backupctl /usr/local/bin/backupctl
COPY --from=build /out/AGE-LICENSE /usr/share/licenses/backupctl/AGE-LICENSE
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/backupctl"]
