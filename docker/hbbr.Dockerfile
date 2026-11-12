ARG TARGETARCH

FROM alpine:3
ARG TARGETARCH
COPY docker/bin/${TARGETARCH}/hbbr /usr/local/bin/
ENV RUST_LOG=info
WORKDIR /root
EXPOSE 21117 21119
VOLUME /root
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD nc -z 127.0.0.1 21117 || exit 1
ENTRYPOINT ["/usr/local/bin/hbbr"]
