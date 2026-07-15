FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /allspeak-catalog ./cmd/allspeak-catalog

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /allspeak-catalog /allspeak-catalog
EXPOSE 8080
ENTRYPOINT ["/allspeak-catalog"]
