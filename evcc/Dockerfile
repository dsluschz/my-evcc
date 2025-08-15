ARG BUILD_FROM=ghcr.io/home-assistant/aarch64-base:latest
FROM ${BUILD_FROM}

# Build-Abhängigkeiten installieren
RUN apk add --no-cache \
    git make curl go yarn nodejs npm bash gcc g++ libc-dev linux-headers

# EVCC klonen und bauen (direkt mit fixer URL statt Variablen)
RUN git clone --branch master https://github.com/dsluschz/my-evcc.git /build \
    && cd /build \
    && go generate ./... \
    && make install
