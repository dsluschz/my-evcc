ARG BUILD_FROM=ghcr.io/home-assistant/aarch64-base:latest
FROM ${BUILD_FROM}

RUN apk add --no-cache git make curl go yarn nodejs npm bash gcc g++ libc-dev linux-headers

# Repo klonen
RUN git clone --branch master https://github.com/dsluschz/my-evcc.git /build

# In den Ordner wechseln, in dem die go.mod liegt (!)
WORKDIR /build/evcc

# Jetzt erst generieren/bauen
RUN go generate ./... && make
