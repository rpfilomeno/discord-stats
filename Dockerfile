# Build
FROM golang:1.26.5-alpine AS build
WORKDIR /src
# only needed for the git rev-parse that stamps the footer version
RUN apk add --no-cache git
COPY go.mod go.sum ./
COPY third_party ./third_party
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
# .git is only here so the footer can name the commit it was built from.
COPY .git ./.git
RUN git config --global --add safe.directory '*' && \
    VERSION="$(git rev-parse --short HEAD 2>/dev/null || echo dev)" && \
    CGO_ENABLED=0 go build -trimpath \
        -ldflags="-s -w -X github.com/rpfilomeno/discord-stats/internal/render.Version=$VERSION" \
        -o /out/statsbot ./cmd/statsbot

# Run
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata su-exec && adduser -D -u 10001 app && \
    mkdir -p /data && chown app:app /data
COPY --from=build /out/statsbot /usr/local/bin/statsbot
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
USER app
VOLUME /data
ENTRYPOINT ["/entrypoint.sh"]
