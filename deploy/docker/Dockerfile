# Minimal runtime image: copy a pre-built CGO-free shoal binary.
# Build context is staged by Ansible (binary + this Dockerfile).
FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY shoal /usr/local/bin/shoal
RUN chmod +x /usr/local/bin/shoal
EXPOSE 8088
ENTRYPOINT ["/usr/local/bin/shoal"]
CMD ["serve", "-addr", ":8088"]
