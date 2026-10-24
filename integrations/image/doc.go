// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

// Package image tests Echo's container image (the Dockerfile at the
// repository root) and the promise behind it: an image that replaces
// Mailpit in Docker Compose, DDEV, Lando and Sail needs Mailpit's API to
// answer in Mailpit's shape. The shape check runs against an in-process
// catcher in every test run; with the docker build tag it builds the image,
// sends a mail over SMTP and checks the same shapes against the container.
// It has no code of its own.
package image
