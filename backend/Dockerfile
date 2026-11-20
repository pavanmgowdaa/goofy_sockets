FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /fairpass-api ./cmd/api
FROM alpine:3.21
RUN adduser -D -H app
COPY --from=build /fairpass-api /usr/local/bin/fairpass-api
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/fairpass-api"]
