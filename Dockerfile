# Build
FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party ./third_party
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/statsbot ./cmd/statsbot

# Run
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 app && \
    mkdir -p /data && chown app:app /data
COPY --from=build /out/statsbot /usr/local/bin/statsbot
USER app
VOLUME /data
ENTRYPOINT ["statsbot"]
