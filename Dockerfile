FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o sonic .

FROM alpine:3.21

RUN apk add --no-cache ca-certificates libcap

COPY --from=builder /build/sonic /usr/local/bin/sonic
COPY --from=builder /build/functions/hello.js /etc/sonic/functions/hello.js
COPY --from=builder /build/channelworkers.yaml /etc/sonic/channelworkers.yaml

RUN mkdir -p /etc/sonic/certs /etc/sonic/functions && \
    chmod -R 755 /etc/sonic && \
    setcap cap_net_admin,cap_net_bind_service,cap_sys_resource+ep /usr/local/bin/sonic 2>/dev/null || true

VOLUME ["/etc/sonic/certs", "/etc/sonic/functions"]
EXPOSE 8443

WORKDIR /etc/sonic
ENTRYPOINT ["sonic"]
CMD ["start"]
