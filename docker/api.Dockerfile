ARG API_VERSION=latest

# ---- Stage 1: build the web console (web/) ----
FROM node:20-alpine AS adminbuild
WORKDIR /src
COPY web/package.json web/package-lock.json ./
RUN npm install
COPY web/ .
RUN npm run build

# ---- Stage 2: build the Go API ----
FROM golang:1.23-alpine AS gobuild
ARG API_VERSION
RUN apk add --no-cache build-base
WORKDIR /src
COPY api/ .
COPY --from=adminbuild /src/dist ./resources/admin
RUN go mod tidy \
 && echo "${API_VERSION#v}" > resources/version \
 && CGO_ENABLED=1 go build -o /out/apimain ./cmd/apimain.go \
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
