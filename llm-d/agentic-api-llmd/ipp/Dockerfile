FROM --platform=$BUILDPLATFORM quay.io/projectquay/golang:1.25 AS builder
WORKDIR /build
COPY go.mod .
COPY main.go .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /ipp .
FROM gcr.io/distroless/static:nonroot
COPY --from=builder /ipp /ipp
ENTRYPOINT ["/ipp"]
