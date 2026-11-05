# Stage 1: build the frontend
FROM node:26-alpine@sha256:c6ad9e9c1d80f6eb607c26b4b0a132ff7a2344fad1c78ea3baa2a7d4c8de6d94 AS frontend
WORKDIR /frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# Stage 2: build the Go binary (embeds frontend/dist)
FROM golang:1.27.3-alpine@sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -trimpath -tags goolm -ldflags="-s -w" -o /out/eve ./cmd/server/

# Stage 3: runtime
FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S eve && adduser -S -G eve eve && \
    mkdir -p /data /app/data && chown -R eve:eve /data /app && chmod 700 /data /app/data
WORKDIR /app
COPY --from=backend /out/eve /app/eve
USER eve
ENV LISTEN=:8090
ENV DATA_DIR=/data
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s CMD wget -q -O /dev/null http://127.0.0.1:8090/healthz || exit 1
EXPOSE 8090
ENTRYPOINT ["/app/eve"]
