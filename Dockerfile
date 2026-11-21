# syntax=docker/dockerfile:1

# The build stages run on the build machine and cross-compile, so a
# multi-platform build needs no emulation.
FROM --platform=$BUILDPLATFORM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/
COPY --from=web /src/web/dist web/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o /bgpd ./cmd/bgpd

# Runs as root: BGP listens on port 179.
FROM gcr.io/distroless/static-debian12
LABEL org.opencontainers.image.source="https://github.com/shamalawy/gobgp" \
      org.opencontainers.image.description="BGP-4 daemon with an embedded web UI" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /bgpd /bgpd
VOLUME /data
EXPOSE 179 8080
ENTRYPOINT ["/bgpd", "-data-dir", "/data"]
CMD ["-http", "0.0.0.0:8080"]
