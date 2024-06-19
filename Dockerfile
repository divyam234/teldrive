
FROM golang:alpine AS builder

RUN apk add --no-cache git unzip curl make bash

WORKDIR /app

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN make build

FROM scratch

WORKDIR /

COPY --from=builder /app/bin/teldrive /teldrive

EXPOSE 8080

ENTRYPOINT ["/teldrive","run","--tg-session-file","/session.db"]