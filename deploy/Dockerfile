FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN bash scripts/generate-desk-ui.sh
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/weazlcloud ./cmd/weazlcloud

FROM restic/restic:0.18.0 AS restic

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 7272 -g "" weazl \
 && mkdir -p /data/tmp \
 && chown -R weazl:weazl /data
COPY --from=build /out/weazlcloud /usr/local/bin/weazlcloud
COPY --from=restic /usr/bin/restic /usr/local/bin/restic
USER 7272:7272
ENV HOME=/data
ENV XDG_CACHE_HOME=/data/cache
ENV TMPDIR=/data/tmp
WORKDIR /data
VOLUME /data
EXPOSE 7272 7273 7274
ENTRYPOINT ["/usr/local/bin/weazlcloud"]
