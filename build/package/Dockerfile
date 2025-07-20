FROM golang:1.24.4 AS builder
WORKDIR /app

COPY go.mod go.sum .
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -mod=readonly -ldflags="-w -s" -o jake-blog ./cmd

RUN ssh-keyscan -H github.com >> /etc/known_hosts

FROM alpine:latest
ARG INTERNAL_PORT
ENV INTERNAL_PORT=${INTERNAL_PORT:-8080}

COPY --from=builder /app/jake-blog /jake-blog
COPY --from=builder /app/web /web

COPY --from=builder /etc/known_hosts /etc/known_hosts

RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup && \
    chown -R appuser:appgroup /jake-blog /web /etc/known_hosts && \
    chmod +x /jake-blog

USER appuser
EXPOSE ${INTERNAL_PORT}
ENTRYPOINT ["/jake-blog"]
