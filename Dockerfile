ARG BUILD_FROM=ghcr.io/hassio-addons/debian-base:6.2.6
# hadolint ignore=DL3006
FROM ${BUILD_FROM}

# Set shell
SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ARG BUILD_ARCH=amd64

RUN \
    apt-get update \
    \
    && apt-get install -y \
        python3-dev=3.9.2-3 \
        python3=3.9.2-3 \
        git \
        python3-pip \
        libgl1 \
        libglib2.0-0 \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

RUN pip3 install \
        tqdm \
        xmltodict \
        Pillow \
        netifaces \
        opencv-python

COPY rootfs /

RUN git clone https://github.com/intx82/a9-v720.git /usr/local/a9-v720

# # Build arguments
# ARG BUILD_ARCH
# ARG BUILD_DATE
# ARG BUILD_DESCRIPTION
# ARG BUILD_NAME
# ARG BUILD_REF
# ARG BUILD_REPOSITORY
# ARG BUILD_VERSION
