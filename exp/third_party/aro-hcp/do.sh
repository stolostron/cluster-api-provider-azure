#!/bin/bash
rm  -rf api

# autorest autorest-config.yaml
# rm -f api/v20240610preview/armredhatopenshifthcp/go.*

mkdir -p api/v20240610preview/armredhatopenshifthcp/
rsync -a --delete ../../../../ARO-HCP/test/sdk/resourcemanager/redhatopenshifthcp/armredhatopenshifthcp/ api/v20240610preview/armredhatopenshifthcp/
rm -f api/v20240610preview/armredhatopenshifthcp/go.*

