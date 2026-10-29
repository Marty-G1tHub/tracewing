#!/usr/bin/env bash
# 用法：scripts/release.sh 0.1.0   （在已打好 tag v0.1.0 的干净工作区上运行）
# 产物写入 releases/v<版本>/（不入库）：tracewing.exe、tracewing-linux-{amd64,arm64}、install.sh、源码 zip、SHA256SUMS
set -euo pipefail
V="${1:?版本号，如 0.1.0}"
cd "$(dirname "$0")/.."
grep -q "const Version = \"$V\"" internal/srv/auth.go || { echo "internal/srv/auth.go 的 Version 不是 $V"; exit 1; }
grep -q "\"ProductVersion\": \"$V\"" winres/winres.json || { echo "winres.json 版本不是 $V"; exit 1; }
grep -q "VERSION=\"\${TRACEWING_VERSION:-v$V}\"" install.sh || { echo "install.sh 默认版本不是 v$V"; exit 1; }
if [ -n "$(gofmt -l .)" ]; then echo "gofmt 未通过"; exit 1; fi
go vet ./... && GOOS=windows go vet ./... && go test ./...
shellcheck install.sh tests/netns_route.sh 2>/dev/null || true
R="releases/v$V"; mkdir -p "$R" dist
export CGO_ENABLED=0
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H=windowsgui -s -w" -o dist/tracewing.exe .
GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/tracewing-linux-amd64 .
GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o dist/tracewing-linux-arm64 .
cp dist/tracewing.exe dist/tracewing-linux-amd64 dist/tracewing-linux-arm64 install.sh "$R/"
git archive --format=zip --prefix="tracewing-v$V/" -o "$R/tracewing-v$V-src.zip" "v$V"
( cd "$R" && sha256sum tracewing.exe tracewing-linux-amd64 tracewing-linux-arm64 install.sh "tracewing-v$V-src.zip" > SHA256SUMS && cat SHA256SUMS )
