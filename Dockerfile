# Build stage: the Go version matches go.mod's go directive.
FROM golang:1.27.3 AS build

WORKDIR /src

# Download modules in their own layer so code changes don't invalidate it.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/www ./cmd/www

# Final stage: distroless static includes the CA certificates feed fetching needs
# and runs as a non-root user. Configuration arrives at runtime, never at build time.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/www /www

EXPOSE 8080

ENTRYPOINT ["/www"]
