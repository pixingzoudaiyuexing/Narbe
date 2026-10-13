FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26.9-bookworm@sha256:c81bf22b71e0336d222546ea3360d8f5dd8dab92b1310ba44abaf63b8ca92cc0 AS build
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ENV CGO_ENABLED=0 GOEXPERIMENT=nosystemcrypto GOTELEMETRY=off
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN go build -trimpath -ldflags='-w' -o /out/narbe ./cmd/narbe && mkdir -p /out/data

FROM scratch
LABEL org.opencontainers.image.title="Narbe" \
      org.opencontainers.image.source="https://github.com/pixingzoudaiyuexing/Narbe" \
      io.narbe.project="Narbe"
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/narbe /narbe
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
ENV NARBE_LISTEN=0.0.0.0:8080 NARBE_DATA_DIR=/data NARBE_LOG_LEVEL=info
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD ["/narbe","healthcheck"]
ENTRYPOINT ["/narbe"]
