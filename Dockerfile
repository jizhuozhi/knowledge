FROM node:18-alpine AS frontend-builder
WORKDIR /frontend
COPY frontend/package.json frontend/package-lock.json* frontend/yarn.lock* frontend/pnpm-lock.yaml* ./
RUN npm install
COPY frontend/ .
RUN npm run build

FROM golang:1.24-alpine AS backend-builder
WORKDIR /app
RUN apk add --no-cache git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main ./cmd/server

FROM alpine:latest
WORKDIR /app
RUN apk --no-cache add ca-certificates tzdata
COPY --from=backend-builder /app/main .
COPY --from=frontend-builder /frontend/dist ./static
RUN mkdir -p /app/uploads
EXPOSE 8080
ENV STATIC_DIR=/app/static
CMD ["./main"]