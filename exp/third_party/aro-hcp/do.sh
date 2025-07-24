#!/bin/bash
rm  -rf api

# autorest autorest-config.yaml
# rm -f api/v20240610preview/generated/go.*

mkdir -p api/v20240610preview/generated/
rsync -a --delete ../../../../ARO-HCP/internal/api/v20240610preview/generated/ api/v20240610preview/generated/

