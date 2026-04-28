ARG BUILDPLATFORM
ARG TARGETPLATFORM
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.0.0-dev

FROM --platform=$BUILDPLATFORM node:22-alpine AS web-build

WORKDIR /app/web

ARG VERSION=0.0.0-dev
ENV VITE_APP_VERSION=${VERSION}

COPY web/package.json web/bun.lock ./
RUN npm install

COPY web ./
RUN npm run build


FROM --platform=$BUILDPLATFORM golang:1.26.2-bookworm AS go-build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=0.0.0-dev

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} go build -trimpath -ldflags="-s -w -X chatgpt2api/internal/version.Version=${VERSION}" -o /out/chatgpt2api ./cmd/chatgpt2api


FROM --platform=$TARGETPLATFORM debian:bookworm-slim AS app

WORKDIR /app
ENV PORT=80

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
COPY --from=web-build /app/web/dist ./web_dist

EXPOSE 80

CMD ["/app/chatgpt2api"]
