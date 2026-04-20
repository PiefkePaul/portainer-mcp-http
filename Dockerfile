FROM golang:1.24.2 AS builder

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ARG TARGETARCH=amd64

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
	go build \
	-trimpath \
	-ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE}" \
	-o /out/portainer-mcp \
	./cmd/portainer-mcp

FROM gcr.io/distroless/base-debian12:nonroot

WORKDIR /app

COPY --from=builder /out/portainer-mcp /usr/local/bin/portainer-mcp
COPY --chown=nonroot:nonroot internal/tooldef/tools.yaml /app/tools.yaml

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/portainer-mcp"]
CMD ["-transport","http","-listen-addr",":8080","-mcp-path","/mcp","-health-path","/healthz","-tools","/app/tools.yaml"]
