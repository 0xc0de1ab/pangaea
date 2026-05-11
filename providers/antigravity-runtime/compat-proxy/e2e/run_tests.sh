#!/bin/bash
set -e

echo "Starting Antigravity Proxy E2E Test Suite..."
docker-compose up --build --abort-on-container-exit
echo "E2E Tests completed."
