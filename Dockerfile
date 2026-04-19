FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# VERSION is injected at build time by the release workflow. Defaults to the
# value baked into internal/version/version.go when building locally.
ARG VERSION
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w ${VERSION:+-X 'github.com/fireball1725/librarium-api/internal/version.Version=${VERSION}'}" \
    -o /librarium-api ./cmd/api
# Pre-create the default cover storage path so it exists as a mount point.
RUN mkdir -p /data/covers

FROM gcr.io/distroless/static-debian12
COPY --from=builder /librarium-api /librarium-api
COPY --from=builder /data /data
EXPOSE 8080
ENTRYPOINT ["/librarium-api"]
