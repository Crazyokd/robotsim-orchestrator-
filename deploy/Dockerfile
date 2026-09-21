FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY cmd cmd
COPY internal internal
RUN go build -o /simenv-world ./cmd/simenv-world

FROM ubuntu:24.04
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl gnupg lsb-release \
    && curl -fsSL https://packages.osrfoundation.org/gazebo.gpg -o /usr/share/keyrings/pkgs-osrf-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/pkgs-osrf-archive-keyring.gpg] http://packages.osrfoundation.org/gazebo/ubuntu-stable $(lsb_release -cs) main" > /etc/apt/sources.list.d/gazebo-stable.list \
    && apt-get update \
    && apt-get install -y --no-install-recommends gz-harmonic \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /simenv-world /usr/local/bin/simenv-world
COPY deploy/assets /opt/simenv
ENV GZ_SIM_RESOURCE_PATH=/opt/simenv/models
ENV GZ_IP=127.0.0.1
ENV GZ_PARTITION=easygo_sim
LABEL simenv.easygo_world_source_sha="05316d350aadc40f2d1e7bbbe2ed09d4a1092eb1"
ENTRYPOINT ["/usr/local/bin/simenv-world"]
