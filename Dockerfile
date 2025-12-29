# Use golang:latest as a builder for the plugin
FROM --platform=$BUILDPLATFORM golang:latest AS plugin-builder

ENV CGO_CFLAGS="-I/usr/local/include -fPIC"
ENV CGO_LDFLAGS="-shared -Wl,-unresolved-symbols=ignore-all"
ENV CGO_ENABLED=1

# Bring TARGETPLATFORM to the build scope
ARG TARGETPLATFORM
ARG BUILDPLATFORM

# Install TARGETPLATFORM parser to translate its value to GOOS, GOARCH, and GOARM
COPY --from=tonistiigi/xx:latest / /
RUN go env

RUN xx-apt update && xx-apt install -y gcc libc6-dev libudev-dev

WORKDIR /app

COPY ./go.mod ./go.mod
COPY ./go.sum ./go.sum

RUN xx-go mod download

COPY ./ ./
RUN xx-go build -ldflags="-s -w" -o usbip-device-plugin && xx-verify usbip-device-plugin


#Start from a new image.
FROM --platform=$TARGETPLATFORM debian:stable-slim


COPY --from=plugin-builder /app/usbip-device-plugin /app/usbip-device-plugin

CMD ["/app/usbip-device-plugin"]