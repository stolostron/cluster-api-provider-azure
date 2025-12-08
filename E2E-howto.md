# ARO HCP Cluster End-to-End Testing Guide

This guide describes how to perform end-to-end testing of ARO (Azure Red Hat OpenShift) HCP cluster deployment using CAPZ (Cluster API Provider Azure).

## Prerequisites

- Management cluster with CAPZ and ASO installed
- Azure credentials configured
- `clusterctl` binary available at `../cluster-api/bin/clusterctl`
- Cluster manifest prepared at `/home/mveber/projekty/capi/cluster-api-installer-aro-STAGE/aro-int3/aro.yaml`

## Test Setup

1. **Create test namespace**:
   ```bash
   oc create namespace int3-env
   ```

2. **Apply credentials and identity**:
   ```bash
   oc apply -f /home/mveber/projekty/capi/cluster-api-installer-aro-STAGE/aro-int3/credentials.yaml
   oc apply -f /home/mveber/projekty/capi/cluster-api-installer-aro-STAGE/aro-int3/is.yaml
   ```

3. **Deploy the cluster**:
   ```bash
   oc apply -f /home/mveber/projekty/capi/cluster-api-installer-aro-STAGE/aro-int3/aro.yaml
   ```

## Monitoring Deployment

### Watch Controller Logs

Monitor CAPZ controller for reconciliation progress:
```bash
oc -n capz-system logs deployments/capz-controller-manager --follow
```

Monitor ASO controller for Azure resource operations:
```bash
oc -n azureserviceoperator-system logs deployments/azureserviceoperator-controller-manager --follow
```

### Check Cluster Status

View cluster resources:
```bash
oc get -f /home/mveber/projekty/capi/cluster-api-installer-aro-STAGE/aro-int3/aro.yaml
```

View detailed cluster status with conditions:
```bash
../cluster-api/bin/clusterctl describe cluster -n int3-env mveber3-int --show-conditions=all
```

### Check AROControlPlane Status

View control plane status including initialization and conditions:
```bash
oc get -n int3-env arocp mveber3-int-control-plane -o yaml
```

Key status fields to monitor:
- `status.initialization.controlPlaneInitialized` - Must be `true` for machine pools to be created
- `status.apiURL` - HCP cluster API endpoint
- `status.consoleURL` - OpenShift console URL (appears after external auth is configured)
- `status.ready` - Overall readiness status
- `status.conditions` - Detailed conditions for each service

### Check Machine Pool Status

View machine pool status:
```bash
oc get -n int3-env aromp mveber3-int-mp-0 -o yaml
```

View ASO node pool resource:
```bash
oc get -n int3-env hcpopenshiftclustersnodepools
```

### Check External Auth Status

View external auth ASO resource:
```bash
oc get -n int3-env hcpopenshiftclustersexternalauths
```

View external auth condition on control plane:
```bash
oc get -n int3-env arocp mveber3-int-control-plane -o jsonpath='{.status.conditions}' | jq '.[] | select(.type == "ExternalAuthReady")'
```

## Expected Deployment Flow

1. **Infrastructure Services** (parallel):
   - Resource group creation
   - Virtual network and subnet creation
   - Network security group creation
   - Key vault creation

2. **Identity Services** (after infrastructure):
   - User-assigned managed identities creation
   - Role assignments configuration

3. **HCP Cluster Provisioning**:
   - HcpOpenShiftCluster ASO resource created
   - Azure provisions the HCP cluster
   - Cluster reaches `provisioningState: Succeeded`
   - API URL becomes available
   - **Control plane marked as initialized** (`controlPlaneInitialized: true`)

4. **Machine Pool Creation** (after control plane initialized):
   - AROMachinePool creates HcpOpenShiftClustersNodePool ASO resource
   - Azure provisions node pools
   - Node pools reach `provisioningState: Succeeded`

5. **External Authentication** (after machine pools ready):
   - HcpOpenShiftClustersExternalAuth ASO resource created
   - Azure configures external auth on the cluster
   - **Console URL appears** in HcpOpenShiftCluster status
   - Console URL synced to AROControlPlane status
   - ExternalAuthReady condition becomes `True`

## Key Conditions to Monitor

### AROControlPlane Conditions

```bash
oc get -n int3-env arocp mveber3-int-control-plane -o jsonpath='{.status.conditions}' | jq
```

Expected conditions (all should be `True` when ready):
- `Ready` - Overall readiness
- `ResourceGroupReady` - Resource group created
- `VNetReady` - Virtual network created
- `SubnetsReady` - Subnets created
- `NetworkSecurityGroupsReady` - NSG created
- `VaultReady` - Key vault created
- `UserIdentitiesReady` - Managed identities created
- `RoleAssignmentReady` - Role assignments configured
- `HcpClusterReady` - HCP cluster provisioned
- `ExternalAuthReady` - External auth configured

### ExternalAuthReady Condition States

The `ExternalAuthReady` condition shows detailed progress:

1. **WaitingForMachinePools**: Waiting for at least one machine pool to be ready
2. **WaitingForExternalAuth**: External auth ASO resource not yet ready in Azure
3. **WaitingForConsoleURL**: External auth ready but console URL hasn't appeared yet
4. **True**: Everything configured successfully, console URL available

## Troubleshooting

### Control Plane Not Initialized

If `controlPlaneInitialized` remains `false`:
- Check HcpClusterReady condition status and reason
- Check HcpOpenShiftCluster resource status:
  ```bash
  oc get -n int3-env hcpopenshiftclusters mveber3-int -o yaml
  ```
- Check controller logs for errors

### Machine Pools Not Creating

If machine pools show "AROControlPlane is not initialized":
- Verify `status.initialization.controlPlaneInitialized` is `true`
- Check if console URL check is blocking (should NOT block after fix)

### External Auth Not Ready

If `ExternalAuthReady` shows waiting states:
- Check the condition reason to understand which stage is pending
- Verify machine pool status
- Check HcpOpenShiftClustersExternalAuth resource status
- Check for console URL in HcpOpenShiftCluster status

### Console URL Missing

If console URL doesn't appear after external auth:
- External auth must be fully configured in Azure
- May take a few minutes for Azure to populate the console URL
- Check HcpOpenShiftClustersExternalAuth is in `Succeeded` state

## Post-Deployment Configuration

### Configure Azure AD Application

After console URL appears, configure the Azure AD application for console authentication:

```bash
cd /home/mveber/projekty/capi/cluster-api-installer-aro-STAGE/aro-int3
./update-app.sh
```

This script:
- Configures redirect URIs with the console URL
- Enables ID token issuance
- Sets up group claims
- Creates required secrets for console authentication

### Access the Cluster

Retrieve kubeconfig:
```bash
oc get secret -n int3-env mveber3-int-kubeconfig -o jsonpath='{.data.value}' | base64 -d > mveber3-int.kubeconfig
```

Access the cluster:
```bash
oc --kubeconfig=mveber3-int.kubeconfig get nodes
```

Access the console:
```bash
# Console URL from status
oc get -n int3-env arocp mveber3-int-control-plane -o jsonpath='{.status.consoleURL}'
```

## Cleanup

Delete the cluster:
```bash
oc delete -f /home/mveber/projekty/capi/cluster-api-installer-aro-STAGE/aro-int3/aro.yaml
```

Watch deletion progress in controller logs and verify all Azure resources are removed.

## Success Criteria

A successful E2E test should show:

1. ✅ All infrastructure resources created
2. ✅ HCP cluster provisioned (`provisioningState: Succeeded`)
3. ✅ Control plane initialized (`controlPlaneInitialized: true`)
4. ✅ Machine pools provisioned and ready
5. ✅ External auth configured
6. ✅ Console URL available and synced
7. ✅ All AROControlPlane conditions `True`
8. ✅ Cluster accessible via kubeconfig
9. ✅ Console accessible after Azure AD configuration

## Notes

- The entire deployment typically takes 15-25 minutes
- External auth configuration requires at least one ready machine pool
- Console URL only appears after external auth is successfully configured
- The controller automatically syncs console URL from HcpOpenShiftCluster to AROControlPlane
