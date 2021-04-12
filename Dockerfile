# Based on the Dockerfile suggested by Codefresh at
# https://codefresh.io/docs/docs/learn-by-example/golang/golang-hello-world/

FROM golang:1-alpine AS build_base
WORKDIR /tmp/smtp-translator
COPY go.mod .
COPY go.sum .
RUN go mod download
COPY . .
RUN go build -o ./out/smtp-translator .

FROM alpine
COPY --from=build_base /tmp/smtp-translator/out/smtp-translator /app/smtp-translator
EXPOSE 25
CMD ["/app/smtp-translator"]
