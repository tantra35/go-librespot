#!/bin/bash

REPOSITORY=tantra35/go-librespot
VERSION=0.0.5

sudo docker build \
  --build-arg TARGET=aarch64-linux-gnu \
  --build-arg GOARCH=arm64 \
  --build-arg VERSION=${VERSION} \
  --build-arg CC=aarch64-linux-gnu-gcc \
  --tag=$REPOSITORY:${VERSION} \
  .

if [ $? -ne 0 ]; then
	echo "build main container failed"
	exit 100
fi

sudo docker save -o ${REPOSITORY//\//-}-$VERSION.tar $REPOSITORY:$VERSION
gzip -f  ${REPOSITORY//\//-}-$VERSION.tar
chmod 0666 ./${REPOSITORY//\//-}-$VERSION.tar.gz
