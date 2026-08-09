FROM golang:1.26-alpine AS build

ARG SERVICE
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked \
    --mount=type=cache,target=/root/.cache/go-build,sharing=locked \
    test -n "$SERVICE" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/service "./cmd/$SERVICE"

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
    && addgroup -S agentmemory \
    && adduser -S -G agentmemory -H agentmemory
USER agentmemory
COPY --from=build /out/service /usr/local/bin/service
ENTRYPOINT ["/usr/local/bin/service"]
