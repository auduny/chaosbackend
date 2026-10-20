FROM golang:1 AS builder
WORKDIR /app
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o chaosbackend

FROM scratch
COPY --from=builder /app/chaosbackend /chaosbackend
USER 65534
EXPOSE 8080
ENTRYPOINT ["/chaosbackend"]
# Listen on all interfaces, otherwise the container is unreachable from outside
CMD ["-a", "0.0.0.0", "-p", "8080"]
