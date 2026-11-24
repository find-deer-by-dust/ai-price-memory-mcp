FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /ai-price-memory-mcp .

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /ai-price-memory-mcp /ai-price-memory-mcp
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/ai-price-memory-mcp"]
