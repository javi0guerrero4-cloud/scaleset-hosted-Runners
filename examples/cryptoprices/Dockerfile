# Development image for the crypto price example. Application source is
# bind-mounted by docker-compose.base44.yml, so nothing from the repo is baked
# in here — the image only provides the Go toolchain and `air` for live reload.
FROM golang:1.25-alpine

RUN go install github.com/air-verse/air@v1.61.7

ENV PATH="/go/bin:${PATH}"

WORKDIR /app/examples/cryptoprices

CMD ["air", "-c", ".air.toml"]
