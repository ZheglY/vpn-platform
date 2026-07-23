ARG GO_VERSION=1.26.5
ARG GO_BUILDER_DIGEST=sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2
ARG DISTROLESS_DIGEST=sha256:aef9602f8710ec12bde19d593fed1f76c708531bb7aba205110f1029786ead7b

FROM golang:${GO_VERSION}-alpine@${GO_BUILDER_DIGEST} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY internal/ internal/
COPY services/notification/ services/notification/
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
    -ldflags="-s -w -X main.buildVersion=${VERSION} -X main.buildCommit=${COMMIT} -X main.buildDate=${DATE}" \
    -o /out/notification-service ./services/notification/cmd/notification-service

FROM gcr.io/distroless/static-debian12:nonroot@${DISTROLESS_DIGEST}
WORKDIR /
COPY --from=build /out/notification-service /notification-service
USER nonroot:nonroot
ENV HTTP_ADDR=:8091
EXPOSE 8091
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD ["/notification-service", "healthcheck"]
ENTRYPOINT ["/notification-service"]
