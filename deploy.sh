#!/bin/bash
set -e
cd "$(dirname "$0")"
git pull
go build -o forager ./cmd/forager
sudo cp forager /usr/local/bin/
sudo systemctl restart forager
sleep 1
curl -sf http://localhost:8090/healthz > /dev/null && echo "deployed: $(git log -1 --oneline)"
