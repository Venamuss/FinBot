FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/finbot ./cmd/finbot

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 finbot
COPY --from=build /out/finbot /usr/local/bin/finbot
USER finbot
EXPOSE 8080
ENTRYPOINT ["finbot"]
