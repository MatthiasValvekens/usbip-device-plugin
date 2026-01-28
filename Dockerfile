# Use golang:latest as a builder for the plugin
FROM --platform=$BUILDPLATFORM golang:alpine AS plugin-builder

# Bring TARGETPLATFORM to the build scope
ARG TARGETPLATFORM
ARG BUILDPLATFORM

# Install TARGETPLATFORM parser to translate its value to GOOS, GOARCH, and GOARM
COPY --from=tonistiigi/xx:latest / /
RUN go env

WORKDIR /app

COPY ./go.mod ./go.mod
COPY ./go.sum ./go.sum

RUN xx-go mod download

COPY ./ ./
RUN xx-go build -o usbip-device-plugin && xx-verify usbip-device-plugin


#Start from a new image.
FROM --platform=$TARGETPLATFORM alpine:latest


COPY --from=plugin-builder /app/usbip-device-plugin /app/usbip-device-plugin

CMD ["/app/usbip-device-plugin"]