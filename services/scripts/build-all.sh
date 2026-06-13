#!/bin/bash
# 构建所有 Go 服务
for svc in api-gateway catalog quote checkout ledger mint chain-adapter provisioning audit; do
  echo "Building $svc..."
  cd services/$svc/cmd && go build -o ../../../bin/$svc . && cd ../../..
done
echo "All services built in bin/"
