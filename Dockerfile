# Build the same command exercised by the acceptance suite.
FROM golang:1.27.0-alpine@sha256:4c9fe60190a2a3350ddc51de80d0224b8a6698d12bdfc999fee45ea9d6c46dbc AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/agent ./cmd/agent

# No shell, package manager or writable secret store.
FROM scratch
COPY --from=builder /out/agent /agent
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY LICENSE /LICENSE
USER 10001:10001
WORKDIR /app
ENTRYPOINT ["/agent"]
