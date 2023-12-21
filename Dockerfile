FROM golang:latest as builder
WORKDIR /app
COPY . .
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o chaosbackend

FROM scratch
COPY --from=builder /app/chaosbackend .
ENTRYPOINT ["./chaosbackend --listen 0.0.0.0:8080"]