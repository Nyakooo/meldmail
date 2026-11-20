# Stage 1: 构建 Vue 3 前端
FROM node:20-alpine AS web-builder
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 2: 构建单二进制 Go 后端（内嵌前端产物）
FROM golang:1.24-alpine AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /src/internal/api/dist ./internal/api/dist
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/meldmail ./cmd/meldmail

# Stage 3: 极简运行镜像
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=go-builder /out/meldmail /app/meldmail
RUN mkdir -p /data

EXPOSE 8080
ENV MELDMAIL_ADDR=:8080 \
    MELDMAIL_DB_PATH=/data/meldmail.db \
    GOMEMLIMIT=200MiB \
    GOGC=50

ENTRYPOINT ["/app/meldmail"]