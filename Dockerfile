# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/orchard ./cmd/orchard

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/orchard /orchard
VOLUME ["/data"]
ENV ORCHARD_DATA=/data
EXPOSE 8080 8443
USER nonroot:nonroot
ENTRYPOINT ["/orchard"]
