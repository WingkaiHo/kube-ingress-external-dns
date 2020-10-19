#!/bin/bash

GOARCH=amd64 CGO_ENABLED=0 go build -ldflags -w  -o kube-ingress-external-dns 
CURR_PATH=`pwd`

sudo docker build -t docker.tupu.ai/tupu/kube-ingress-host-dns:v1.6 $CURR_PATH
