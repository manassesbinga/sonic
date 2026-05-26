#!/bin/bash
set -e

export PATH=/root/go-1.24/bin:$PATH
cd /root/sonic

echo "Cleaning up old builds..."
rm -rf release
mkdir -p release/amd64 release/arm64

echo "Building for Linux amd64..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o release/amd64/sonic main.go
tar -czvf release/sonic-linux-amd64.tar.gz -C release/amd64 sonic

echo "Building for Linux arm64..."
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o release/arm64/sonic main.go
tar -czvf release/sonic-linux-arm64.tar.gz -C release/arm64 sonic

echo "Releases generated in /root/sonic/release:"
ls -lh release/*.tar.gz
