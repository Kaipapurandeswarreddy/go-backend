# Ambigo Go Backend — local + production
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /ambigo-server ./cmd/server

FROM alpine:3.20
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /ambigo-server /app/ambigo-server
COPY migrations ./migrations
EXPOSE 8080
CMD ["/app/ambigo-server"]
