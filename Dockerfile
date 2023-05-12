ARG BUILD_FROM=ghcr.io/hassio-addons/base:14.0.0
# hadolint ignore=DL3006
FROM ${BUILD_FROM}

# Set shell
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ARG BUILD_ARCH=amd64


RUN \
   apk add --no-cache \
        python3 \
        ffmpeg \
        py3-pip 

RUN pip3 install \
        netifaces 


COPY rootfs /
