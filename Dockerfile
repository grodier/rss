# Assets stage: the Node version matches web/.node-version.
FROM node:26-slim AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Build stage: the Go version matches go.mod's go directive.
FROM golang:1.27.2 AS build

WORKDIR /src

# Download modules in their own layer so code changes don't invalidate it.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web /web/dist ./web/dist

ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/www ./cmd/www

# Final stage: distroless static includes the CA certificates feed fetching needs
# and runs as a non-root user. Configuration arrives at runtime, never at build time.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/www /www

EXPOSE 8080

ENTRYPOINT ["/www"]
