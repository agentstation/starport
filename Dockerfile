FROM golang:1.26.5-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/starport ./cmd/starport \
    && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build --chown=65532:65532 /out/starport /usr/local/bin/starport
COPY --from=build --chown=65532:65532 /out/data /var/lib/starport/data

WORKDIR /var/lib/starport
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/starport"]
