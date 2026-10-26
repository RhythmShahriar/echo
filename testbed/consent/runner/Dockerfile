# Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
# SPDX-License-Identifier: MIT

# The consent testbed's runner: Chrome for Testing at the version and
# SHA-256 Echo pins (internal/browser/cft_pins.go; TestRunnerPinsMatch
# checks they agree), GNU time for the memory measure and NSS tools to
# trust the testbed CA. rhythm-echo itself is mounted from runtime/ at
# start, so a rebuild of Echo needs no rebuild of this image.
FROM debian:bookworm-slim

ARG CFT_VERSION=156.0.8078.47
ARG CFT_SHA256=e6c94490f0b6b337225aeb2d2a5034858516b7017356980ba94f5176d8774a50

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates curl unzip time libnss3-tools \
		libasound2 libatk-bridge2.0-0 libatk1.0-0 libatspi2.0-0 libcairo2 libcups2 libdbus-1-3 libdrm2 \
		libexpat1 libgbm1 libglib2.0-0 libnspr4 libnss3 libpango-1.0-0 libx11-6 libxcb1 libxcomposite1 \
		libxdamage1 libxext6 libxfixes3 libxkbcommon0 libxrandr2 fonts-liberation fonts-noto-core \
	&& curl -fsSL -o /tmp/chrome.zip "https://storage.googleapis.com/chrome-for-testing-public/${CFT_VERSION}/linux64/chrome-linux64.zip" \
	&& echo "${CFT_SHA256}  /tmp/chrome.zip" | sha256sum -c - \
	&& unzip -q /tmp/chrome.zip -d /opt \
	&& ln -s /opt/chrome-linux64/chrome /usr/local/bin/chrome \
	&& rm /tmp/chrome.zip \
	&& apt-get purge -y curl unzip && apt-get autoremove -y && rm -rf /var/lib/apt/lists/*

COPY entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
CMD ["sleep", "infinity"]
