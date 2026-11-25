# Stage 1: 构建 Vue 3 前端
FROM --platform=$BUILDPLATFORM node:20-alpine AS web-builder
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: 构建单二进制 Go 后端（内嵌前端产物）
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS go-builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /src/internal/api/dist ./internal/api/dist
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/meldmail ./cmd/meldmail

# Stage 3: 极简运行镜像
FROM alpine:3.20
LABEL io.meldmail.project="meldmail" \
    org.opencontainers.image.source="https://github.com/Nyakooo/meldmail" \
    org.opencontainers.image.licenses="Apache-2.0"
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=go-builder /out/meldmail /app/meldmail
COPY LICENSE THIRD_PARTY_NOTICES.md /app/
RUN mkdir -p /data

EXPOSE 8080
ENV MELDMAIL_ADDR=:8080 \
    MELDMAIL_DB_PATH=/data/meldmail.db \
    GOMEMLIMIT=200MiB \
    GOGC=50

ENTRYPOINT ["/app/meldmail"]
