#!/bin/bash

. ./compile.perpare.sh

sudo docker run --rm -v $PWD:/src -e GOOUTSUFFIX=-arm64 go-librespot-build-arm64
