FROM --platform=$BUILDPLATFORM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS build

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build \
    -trimpath \
    -buildvcs=false \
    -ldflags="-s -w -X main.version=$VERSION -X main.buildTime=$BUILD_TIME -X main.gitCommit=$COMMIT -X main.gitBranch=release" \
    -o /out/starport ./cmd/starport \
    && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown

LABEL org.opencontainers.image.title="Starport" \
    org.opencontainers.image.description="OpenAI- and OpenRouter-compatible LLM inference gateway" \
    org.opencontainers.image.created="$BUILD_TIME" \
    org.opencontainers.image.revision="$COMMIT" \
    org.opencontainers.image.version="$VERSION" \
    org.opencontainers.image.source="https://github.com/agentstation/starport" \
    org.opencontainers.image.licenses="AGPL-3.0-only"

COPY --from=build --chown=65532:65532 /out/starport /usr/local/bin/starport
COPY --from=build --chown=65532:65532 /out/data /var/lib/starport/data

WORKDIR /var/lib/starport
USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/starport"]
