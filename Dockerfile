FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26.11-bookworm@sha256:b40d917d1b6dcc11590cb3986a498f4858a229feab9f130d3acf68d6db28aa74 AS build
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ENV CGO_ENABLED=0 GOEXPERIMENT=nosystemcrypto GOTELEMETRY=off
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN go build -trimpath -ldflags='-w' -o /out/narbe ./cmd/narbe && mkdir -p /out/data

FROM scratch
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="Narbe" \
      org.opencontainers.image.source="https://github.com/pixingzoudaiyuexing/Narbe" \
      io.narbe.project="Narbe" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/narbe /narbe
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
ENV NARBE_LISTEN=0.0.0.0:8080 NARBE_DATA_DIR=/data NARBE_LOG_LEVEL=info
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/narbe","healthcheck"]
ENTRYPOINT ["/narbe"]
