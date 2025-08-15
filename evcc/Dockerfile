ARG BUILD_FROM=ghcr.io/home-assistant/aarch64-base:latest
FROM $BUILD_FROM

ENV LANG C.UTF-8 \
    TZ=Europe/Berlin \
    EVCC_REPO=https://github.com/dsluschz/EVCC-QAB.git \
    EVCC_BRANCH=master

# Tools installieren
RUN apk add --no-cache git make curl go yarn nodejs npm bash gcc g++ libc-dev linux-headers

# EVCC Repository klonen und bauen
RUN git clone --branch ${EVCC_BRANCH} ${EVCC_REPO} /build \
    && cd /build \
    && go generate ./... \
    && make install

# Ports
EXPOSE 5353/udp 4712/tcp 7070/tcp 7090/udp 8887/tcp 8899/udp 9522/udp

# run.sh kopieren und ausführbar machen
COPY run.sh /run.sh
RUN chmod +x /run.sh

CMD [ "/run.sh" ]
