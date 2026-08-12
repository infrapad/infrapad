FROM golang:1.22-alpine AS build
WORKDIR /app
COPY go.mod main.go ./
RUN go build -o monitored-app .

FROM alpine:3.20
COPY --from=build /app/monitored-app /usr/local/bin/monitored-app
EXPOSE 8080
CMD ["monitored-app"]
