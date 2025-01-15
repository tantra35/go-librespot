#!/bin/bash

sudo docker build \
  --build-arg TARGET=aarch64-linux-gnu \
  --build-arg GOARCH=arm64 \
  --build-arg CC=aarch64-linux-gnu-gcc \
  -f Dockerfile.build \
  -t go-librespot-build-arm64 \
  .
