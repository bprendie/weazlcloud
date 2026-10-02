FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN bash scripts/generate-desk-ui.sh
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/weazlcloud ./cmd/weazlcloud
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/takeout-layout ./cmd/takeout-layout
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/weazl-photo-worker ./cmd/weazl-photo-worker

FROM restic/restic:0.18.0 AS restic

FROM golang:1.25-bookworm AS metadata-reader-build
WORKDIR /src
COPY native/restic-reader/go.mod native/restic-reader/go.sum ./
RUN go mod download
COPY native/restic-reader/*.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /weazl-restic-reader .

FROM alpine:3.21 AS preview-build
RUN apk add --no-cache build-base libjpeg-turbo-dev
COPY native/preview/main.c /src/main.c
RUN cc -O3 -Wall -Wextra -Werror -o /weazl-preview-turbo /src/main.c -ljpeg

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata libjpeg-turbo ffmpeg libheif-tools \
 && adduser -D -H -u 7272 -g "" weazl \
 && mkdir -p /data/tmp /run/weazlcloud-photo \
 && chown -R weazl:weazl /data /run/weazlcloud-photo
COPY --from=build /out/weazlcloud /usr/local/bin/weazlcloud
COPY --from=build /out/takeout-layout /usr/local/bin/takeout-layout
COPY --from=build /out/weazl-photo-worker /usr/local/bin/weazl-photo-worker
COPY --from=restic /usr/bin/restic /usr/local/bin/restic
COPY --from=metadata-reader-build /weazl-restic-reader /usr/local/bin/weazl-restic-reader
COPY --from=preview-build /weazl-preview-turbo /usr/local/bin/weazl-preview-turbo
COPY docs/licenses/thumbhash-ISC.txt /usr/share/licenses/weazlcloud/thumbhash-ISC.txt
USER 7272:7272
ENV HOME=/data
ENV XDG_CACHE_HOME=/data/cache
ENV TMPDIR=/data/tmp
WORKDIR /data
VOLUME /data
EXPOSE 7272 7273 7274
ENTRYPOINT ["/usr/local/bin/weazlcloud"]
