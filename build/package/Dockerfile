FROM golang:1.25.6 AS builder
WORKDIR /app

COPY go.mod go.sum .
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -mod=readonly -ldflags="-w -s" -o jake-blog ./cmd

RUN ssh-keyscan -H github.com >> /etc/known_hosts

FROM alpine:latest

RUN apk add --no-cache libcap

RUN addgroup -g 2200 jakeblog \
 && adduser -D -u 2100 -G jakeblog jakeblog

COPY --from=builder /app/jake-blog /jake-blog
COPY --from=builder /app/web /web

COPY --from=builder /etc/known_hosts /etc/known_hosts

RUN setcap 'cap_net_bind_service=+ep' /jake-blog

USER jakeblog

EXPOSE 443
ENTRYPOINT ["/jake-blog"]
