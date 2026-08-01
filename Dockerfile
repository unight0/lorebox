FROM golang:alpine AS builder
WORKDIR /build
COPY . .
RUN go build -o /lorebox .

FROM alpine
RUN apk update && apk upgrade && apk add git
WORKDIR /lorebox
COPY --from=builder /lorebox /usr/bin/lorebox
COPY --from=builder /build/lorebox.yml /lorebox.yml

CMD ["lorebox", "serve", "-config", "/lorebox.yml", "-root", "/lorebox" ]
