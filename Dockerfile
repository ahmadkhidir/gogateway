# GoGateway — Multi-stage Docker build
#
# Build stage: compile the static binary.
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /gogateway ./cmd/gogateway

# Run stage: minimal runtime with CA certs for HTTPS upstreams.
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /gogateway /usr/local/bin/gogateway
COPY gogateway.yaml /etc/gogateway/gogateway.yaml
COPY api-keys.yaml /etc/gogateway/api-keys.yaml

EXPOSE 8080 9090

ENV GOGATEWAY_JWT_SECRET=""
ENV GOGATEWAY_API_KEY_FILE="/etc/gogateway/api-keys.yaml"

ENTRYPOINT ["/usr/local/bin/gogateway"]
CMD ["--config-path", "/etc/gogateway/gogateway.yaml"]
