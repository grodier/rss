# Assets stage: the Node version matches web/.node-version.
FROM node:26-slim AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Build stage: the Go version matches go.mod's go directive.
FROM golang:1.26.5 AS build

WORKDIR /src

# Download modules in their own layer so code changes don't invalidate it.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web /web/dist ./web/dist

ARG VERSION=dev
# One build for both images, so the reader and the admin always run the same
# commit; -o into a directory names each after its package (/out/www, /out/admin).
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/ ./cmd/www ./cmd/admin

# The admin's image (docker build --target admin), for its own service. Unlike
# www's it has a shell, so the admin's commands (admin enroll, admin invite) run
# from Render's Shell: Render's Shell needs a login shell for the running user
# and a ~/.ssh it owns, which distroless images don't have. See "Admin" in
# docs/decisions.md.
FROM alpine:3.24 AS admin

# Alpine's base already has the CA certificates (ca-certificates-bundle) the
# admin's SMTP connection needs, so it installs no packages.
RUN adduser -D -u 10001 -s /bin/sh admin \
 && install -d -m 0700 -o admin -g admin /home/admin/.ssh

COPY --from=build /out/admin /admin

USER admin
EXPOSE 8080

ENTRYPOINT ["/admin"]

# Final stage, the default target (www's image): distroless static includes the
# CA certificates feed fetching needs and runs as a non-root user.
# Configuration arrives at runtime, never at build time.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/www /www

EXPOSE 8080

ENTRYPOINT ["/www"]
