FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/nicktill/tinyobs/pkg/server.Version=${VERSION}" -o /out/tinyobs ./cmd/tinyobs \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/tinyobs-example ./cmd/example

FROM alpine:3.20
RUN adduser -D -H -u 10001 tinyobs && mkdir /data && chown tinyobs /data
COPY --from=build /out/ /usr/local/bin/
USER tinyobs
# Inside a container, listen on all interfaces; publish the ports you need.
ENV TINYOBS_LISTEN=:8421 TINYOBS_OTLP_LISTEN=:4318 TINYOBS_DATA_DIR=/data
VOLUME /data
EXPOSE 8421 4318
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s CMD wget -q --spider http://localhost:8421/-/healthy || exit 1
ENTRYPOINT ["tinyobs"]
