# Multi-stage build for matrix2acrobits
FROM golang:1.25.6-alpine AS builder
WORKDIR /src

# Cache go mod
COPY go.mod go.sum ./
RUN go env -w GOPROXY=https://proxy.golang.org && go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags='-s -w' -o /out/matrix2acrobits ./main.go

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/matrix2acrobits /usr/local/bin/matrix2acrobits

EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/matrix2acrobits"]
