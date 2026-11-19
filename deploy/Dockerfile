FROM docker.io/library/alpine:3.23
RUN apk add --no-cache ca-certificates tzdata
COPY drive /usr/local/bin/drive
USER 10002:10002
ENTRYPOINT ["/usr/local/bin/drive"]
