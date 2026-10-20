# Stage 1: build the frontend
FROM node:26-alpine@sha256:6f08a523443ddedf52941874c203475b0ad503e859ad70cb26ab8be9a517dbd5 AS frontend
WORKDIR /frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY frontend/ ./
RUN npm run build

# Stage 2: build the Go binary (embeds frontend/dist)
FROM golang:1.27.2-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /frontend/dist ./frontend/dist
RUN CGO_ENABLED=0 go build -trimpath -tags goolm -ldflags="-s -w" -o /out/eve ./cmd/server/

# Stage 3: runtime
FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
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
