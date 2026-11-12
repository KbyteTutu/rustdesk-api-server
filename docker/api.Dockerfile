ARG ADMIN_WEB_REF=3998c2a9213fcd047252776d0f0db33e6717026c
ARG API_VERSION=latest

# ---- Stage 1: build the web console (resources/admin) ----
FROM node:20-alpine AS adminbuild
ARG ADMIN_WEB_REF
RUN apk add --no-cache git
RUN git clone https://github.com/lejianwen/rustdesk-api-web.git /src \
 && cd /src \
 && git checkout ${ADMIN_WEB_REF} \
 && npm install \
 && npm run build

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
