# syntax=docker/dockerfile:1.7

ARG VERSION=0.0.0-dev

FROM --platform=$BUILDPLATFORM oven/bun:1-alpine AS web-deps

WORKDIR /app/web

COPY web/package.json web/bun.lock ./
RUN --mount=type=cache,target=/root/.bun/install/cache,sharing=locked \
    bun install --frozen-lockfile


FROM web-deps AS web-build

COPY web ./
ARG VERSION=0.0.0-dev
ENV VITE_APP_VERSION=${VERSION}
RUN bun run build


FROM --platform=$BUILDPLATFORM golang:1.26.2-bookworm AS go-build

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod,sharing=locked go mod download

COPY cmd ./cmd
COPY internal ./internal
COPY --from=web-build /app/internal/web/dist ./internal/web/dist
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.0.0-dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -tags=embed -ldflags="-s -w -X chatgpt2api/internal/version.Version=${VERSION}" -o /out/chatgpt2api ./cmd/chatgpt2api


FROM --platform=$TARGETPLATFORM debian:bookworm-slim AS app

WORKDIR /app
ENV PORT=80
ENV CHATGPT2API_DEPLOYMENT=docker

# 运行时依赖：
# - ca-certificates: HTTPS 上游请求需要
# - git: Git 存储后端需要
# - tzdata: 保持容器内时区数据可用
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    git \
    tzdata \
    && rm -rf /var/lib/apt/lists/*

COPY --from=go-build /out/chatgpt2api ./chatgpt2api

EXPOSE 80

CMD ["/app/chatgpt2api"]
