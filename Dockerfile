# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG VCS_REF=unknown
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w -X github.com/layou233/zbproxy/v3/version.CommitHash=${VCS_REF}" \
    -o /out/zbproxy ./cmd/zbproxy

FROM gcr.io/distroless/static-debian12:nonroot

LABEL org.opencontainers.image.source="https://github.com/SoHugePenguin/ZBProxy" \
      org.opencontainers.image.licenses="Apache-2.0"

COPY --from=build /out/zbproxy /usr/local/bin/zbproxy
COPY --chown=65532:65532 deploy/ZBProxy.example.json /data/ZBProxy.json

WORKDIR /data
VOLUME ["/data"]
EXPOSE 25565/tcp 8080/tcp
ENTRYPOINT ["/usr/local/bin/zbproxy"]
