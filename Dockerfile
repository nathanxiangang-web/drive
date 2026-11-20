ARG GO_IMAGE=docker.io/library/golang:1.27.4-alpine3.24
ARG RUNTIME_IMAGE=docker.io/library/alpine:3.23
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags timetzdata -trimpath -ldflags="-s -w" -o /out/drive ./cmd/drive

FROM ${RUNTIME_IMAGE}
LABEL org.opencontainers.image.source="https://github.com/nathanxiangang-web/drive"
COPY --from=build /out/drive /usr/local/bin/drive
COPY LICENSE /usr/share/licenses/drive/LICENSE
USER 10002:10002
EXPOSE 8098
ENTRYPOINT ["/usr/local/bin/drive"]
