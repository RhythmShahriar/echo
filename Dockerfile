# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# Echo's container image: ghcr.io/rhythmshahriar/echo. A drop-in for
# Mailpit in Docker Compose, DDEV, Lando and Sail: the inbox and API on
# 8025, the SMTP sink on 1025.
#
# The build context holds the static binary for each platform at
# linux/<arch>/rhythm-echo, the layout GoReleaser's dockers_v2 uses. The
# release workflow and integrations/image fill it the same way, so there is
# no Go toolchain in here and nothing runs at build time (no emulation for
# arm64).
#
#   docker run -p 127.0.0.1:8025:8025 -p 127.0.0.1:1025:1025 ghcr.io/rhythmshahriar/echo:1
#
# Without RHYTHM_ECHO_TOKEN, --private-network lets Echo listen without a
# token on private (RFC 1918) and container networks only. Set the token to
# reach Echo from anywhere else: the API then wants it in X-Echo-Token and
# the SMTP sink as the password. Arguments replace the default flags:
#   docker run ... ghcr.io/rhythmshahriar/echo:1 --observe --listen=0.0.0.0:8025

# gcr.io/distroless/static:nonroot (multi-arch index), by digest so a moved
# tag can't change the image. Dependabot bumps it.
FROM gcr.io/distroless/static:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99

ARG TARGETPLATFORM
COPY $TARGETPLATFORM/rhythm-echo /usr/local/bin/rhythm-echo

# Runs as nonroot (65532). Echo keeps its proxy CA (with the private key) in
# /home/nonroot/.config/rhythm/echo/ca; mount a volume on
# /home/nonroot/.config/rhythm/echo to keep it across restarts. WORKDIR
# makes that folder owned by nonroot, so a new volume mounted on it is
# writable too (there is no shell here to mkdir with).
#
# Without a token, --private-network also lets another container name Echo
# by its service name or IP address (http://echo:8025). A router's name,
# such as <project>.ddev.site, needs --hostname or RHYTHM_ECHO_HOSTNAMES.
USER nonroot:nonroot
# There is no browser in here, so consent says so (and how to run it on
# the host) rather than offer to download Chrome.
ENV RHYTHM_ECHO_IMAGE=1
WORKDIR /home/nonroot/.config/rhythm/echo
WORKDIR /home/nonroot
EXPOSE 8025 1025
ENTRYPOINT ["/usr/local/bin/rhythm-echo", "up"]
CMD ["--listen=0.0.0.0:8025", "--smtp=0.0.0.0:1025", "--private-network"]
