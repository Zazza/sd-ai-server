FROM golang:1.25-alpine AS builder
ARG VERSION=dev
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o sd-studio-server .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /build/sd-studio-server .
COPY server-config.yaml ./server-config.yaml
EXPOSE 8080
VOLUME /app/data
ENTRYPOINT ["./sd-studio-server"]
