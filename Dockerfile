# syntax=docker/dockerfile:1

FROM node:20-alpine AS web
WORKDIR /src/web
RUN npm install -g pnpm@9
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.26-alpine AS server
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false \
    -ldflags="-s -w" -o /out/nodehub-server ./server \
    && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -buildvcs=false \
    -ldflags="-s -w" -o /out/nodehub-agent-linux-amd64 ./agent \
    && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -buildvcs=false \
    -ldflags="-s -w" -o /out/nodehub-agent-linux-arm64 ./agent

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S nodehub \
    && adduser -S -G nodehub -h /app nodehub
WORKDIR /app
COPY --from=server /out/nodehub-server /app/nodehub-server
COPY --from=server /out/nodehub-agent-linux-amd64 /app/bin/nodehub-agent-linux-amd64
COPY --from=server /out/nodehub-agent-linux-arm64 /app/bin/nodehub-agent-linux-arm64
COPY --from=web /src/web/dist /app/web
RUN mkdir -p /data/backups && chown -R nodehub:nodehub /app /data
USER nodehub
ENV NODEHUB_DB=/data/nodehub.db
ENV NODEHUB_WEB_DIR=/app/web
ENV NODEHUB_BACKUP_DIR=/data/backups
ENV NODEHUB_AGENT_BINARY=/app/bin/nodehub-agent-linux-amd64
ENV NODEHUB_AGENT_ARM64_BINARY=/app/bin/nodehub-agent-linux-arm64
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/app/nodehub-server"]
CMD ["--addr", "0.0.0.0:8080"]
