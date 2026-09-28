FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN bash scripts/generate-desk-ui.sh
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/weazlcloud ./cmd/weazlcloud
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/takeout-layout ./cmd/takeout-layout

FROM restic/restic:0.18.0 AS restic

FROM alpine:3.21 AS preview-build
RUN apk add --no-cache build-base libjpeg-turbo-dev
COPY native/preview/main.c /src/main.c
RUN cc -O3 -Wall -Wextra -Werror -o /weazl-preview-turbo /src/main.c -ljpeg

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata libjpeg-turbo \
 && adduser -D -H -u 7272 -g "" weazl \
 && mkdir -p /data/tmp \
 && chown -R weazl:weazl /data
COPY --from=build /out/weazlcloud /usr/local/bin/weazlcloud
COPY --from=build /out/takeout-layout /usr/local/bin/takeout-layout
COPY --from=restic /usr/bin/restic /usr/local/bin/restic
COPY --from=preview-build /weazl-preview-turbo /usr/local/bin/weazl-preview-turbo
USER 7272:7272
ENV HOME=/data
ENV XDG_CACHE_HOME=/data/cache
ENV TMPDIR=/data/tmp
WORKDIR /data
VOLUME /data
EXPOSE 7272 7273 7274
ENTRYPOINT ["/usr/local/bin/weazlcloud"]
