ARG API_VERSION=latest

# ---- Stage 1: build the web console (web/) ----
FROM --platform=$BUILDPLATFORM node:20-alpine AS adminbuild
WORKDIR /src
COPY web/package.json web/package-lock.json ./
RUN npm install
COPY web/ .
RUN npm run build

# ---- Stage 2: build the Go API ----
# 始终在构建机原生架构上编译；TARGETARCH=arm64 时用 zig 交叉编译 CGO/musl
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS gobuild
ARG API_VERSION
ARG TARGETARCH
RUN apk add --no-cache build-base zig
WORKDIR /src
COPY api/ .
COPY --from=adminbuild /src/dist ./resources/admin
RUN go mod tidy \
 && echo "${API_VERSION#v}" > resources/version \
 && if [ "$TARGETARCH" = "arm64" ]; then \
        CC="zig cc -target aarch64-linux-musl" CGO_ENABLED=1 GOARCH=arm64 go build -o /out/apimain ./cmd/apimain.go; \
    else \
        CGO_ENABLED=1 go build -o /out/apimain ./cmd/apimain.go; \
    fi \
 && cp -ar resources conf docs /out/ \
 && mkdir -p /out/runtime /out/data

# ---- Stage 3: runtime ----
FROM alpine:3
COPY --from=gobuild /out /app
WORKDIR /app
ENV TZ=Asia/Shanghai
EXPOSE 21114
VOLUME /app/data
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD wget -q -O /dev/null http://127.0.0.1:21114/api/login-options || exit 1
ENTRYPOINT ["./apimain"]
