FROM golang:alpine AS builder
WORKDIR /build
COPY . .
RUN go build -o /lorebox .

FROM alpine:3
WORKDIR /lorebox
COPY --from=builder /lorebox /usr/bin/lorebox
COPY --from=builder /build/lorebox.yml /lorebox.yml
RUN apk update && apk upgrade && apk add --no-cache git=2.54.0-r0
RUN adduser -D lorebox &&\
    chmod 400 /lorebox.yml&&\
    chown -R lorebox:lorebox /lorebox.yml /lorebox

USER lorebox
EXPOSE 8080
CMD ["lorebox", "serve", "-config", "/lorebox.yml", "-root", "/lorebox" ]
