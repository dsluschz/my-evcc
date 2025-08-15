ARG BUILD_FROM=ghcr.io/home-assistant/aarch64-base:latest
FROM $BUILD_FROM

ENV LANG=C.UTF-8 \
    EVCC_REPO=https://github.com/dsluschz/EVCC-QAB.git \
    EVCC_BRANCH=master

# Tools installieren
RUN apk add --no-cache git make curl go yarn nodejs npm bash gcc g++ libc-dev linux-headers

# EVCC klonen und bauen
RUN git clone --branch ${EVCC_BRANCH} ${EVCC_REPO} /build \
    && cd /build \
    && go generate ./... \
    && make install

# Ports freigeben
EXPOSE 7070/tcp \
       5353/udp \
       4712/tcp \
       7090/udp \
       8887/tcp \
       8899/udp \
       9522/udp

# Standard-Startbefehl
CMD ["evcc"]
