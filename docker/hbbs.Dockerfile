ARG TARGETARCH

FROM alpine:3
ARG TARGETARCH
COPY docker/bin/${TARGETARCH}/hbbs docker/bin/${TARGETARCH}/rustdesk-utils /usr/local/bin/
ENV RUST_LOG=info
WORKDIR /root
EXPOSE 21115 21116 21116/udp 21118
VOLUME /root
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
  CMD nc -z 127.0.0.1 21116 || exit 1
ENTRYPOINT ["/usr/local/bin/hbbs"]
CMD ["-r", "hbbr:21117", "-k", "_"]
