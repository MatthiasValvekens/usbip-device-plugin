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

# Install needed libc and gcc for target platform.
RUN set -ex; \
  if [ ! -z "$TARGETPLATFORM" ]; then \
    case "$TARGETPLATFORM" in \
  "linux/amd64") \
    apt update && apt install -y gcc-x86-64-linux-gnu libc6-dev-amd64-cross libudev-dev \
    ;; \
  "linux/arm64") \
    apt update && apt install -y gcc-aarch64-linux-gnu libc6-dev-arm64-cross libudev-dev \
    ;; \
  "linux/arm/v7") \
    apt update && apt install -y gcc-arm-linux-gnueabihf libc6-dev-armhf-cross libudev-dev \
    ;; \
  "linux/arm/v6") \
    apt update && apt install -y gcc-arm-linux-gnueabihf libc6-dev-armel-cross libc6-dev-armhf-cross libudev-dev \
    ;; \
  esac \
  fi

WORKDIR /app

COPY ./go.mod ./go.mod
COPY ./go.sum ./go.sum

RUN go mod download

COPY ./ ./
RUN go build -ldflags="-s -w" -o usbip-device-plugin


#Start from a new image.
FROM debian:stable-slim


COPY --from=plugin-builder /app/usbip-device-plugin /app/usbip-device-plugin

CMD ["/app/usbip-device-plugin"]