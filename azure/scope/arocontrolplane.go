/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package scope

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/keyvault/armkeyvault"
	asoauthorizationv1api20220401 "github.com/Azure/azure-service-operator/v2/api/authorization/v1api20220401"
	"github.com/Azure/azure-service-operator/v2/api/keyvault/v1api20230701"
	asomanagedidentityv1api20230131 "github.com/Azure/azure-service-operator/v2/api/managedidentity/v1api20230131"
	asonetworkv1api20201101 "github.com/Azure/azure-service-operator/v2/api/network/v1api20201101"
	asoresourcesv1 "github.com/Azure/azure-service-operator/v2/api/resources/v1api20200601"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/secret"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	"sigs.k8s.io/cluster-api-provider-azure/azure"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/groups"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/hcpopenshiftclustercredentials"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/hcpopenshiftclusters"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/keyvaults"
	networksecutitygroup "sigs.k8s.io/cluster-api-provider-azure/azure/services/networksecuritygroups"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/roleassignmentsaso"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/subnets"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/userassignedidentities"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/vaults"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/virtualnetworks"
	cplane "sigs.k8s.io/cluster-api-provider-azure/exp/api/controlplane/v1beta2"
	arohcp "sigs.k8s.io/cluster-api-provider-azure/exp/third_party/aro-hcp/api/v20240610preview/generated"
	"sigs.k8s.io/cluster-api-provider-azure/util/futures"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
)

const (
	kubeconfigRefreshNeededValue = "true"
)

// Role definition IDs for ARO HCP cluster role assignments.
type roleDEF string

const (
	roleDEFhcpClusterAPIProvider     = roleDEF("88366f10-ed47-4cc0-9fab-c8a06148393e")
	roleDEFreader                    = roleDEF("acdd72a7-3385-48ef-bd42-f606fba81ae7")
	roleDEFhcpControlPlaneOperator   = roleDEF("fc0c873f-45e9-4d0d-a7d1-585aab30c6ed")
	roleDEFcloudControllerManager    = roleDEF("a1f96423-95ce-4224-ab27-4e3dc72facd4")
	roleDEFingressOperator           = roleDEF("0336e1d3-7a87-462b-b6db-342b63f7802c")
	roleDEFfileStorageOperator       = roleDEF("0d7aedc0-15fd-4a67-a412-efad370c947e")
	roleDEFnetworkOperator           = roleDEF("be7a6435-15ae-4171-8f30-4a343eff9e8f")
	roleDEFfederatedCredentials      = roleDEF("ef318e2a-8334-4a05-9e4a-295a196c6a6e")
	roleDEFhcpServiceManagedIdentity = roleDEF("c0ff367d-66d8-445e-917c-583feb0ef0d4")
	roleDEFkeyVaultCryptoUserRoleID  = roleDEF("12338af0-0e69-4776-bea7-57ae8d297424")
)

// AROControlPlaneScopeParams defines the input parameters used to create a new Scope.
type AROControlPlaneScopeParams struct {
	AzureClients
	Client          client.Client
	Cluster         *clusterv1.Cluster
	ControlPlane    *cplane.AROControlPlane
	Cache           *AROControlPlaneCache
	Timeouts        azure.AsyncReconciler
	CredentialCache azure.CredentialCache
}

// NewAROControlPlaneScope creates a new Scope from the supplied parameters.
// This is meant to be called for each reconcile iteration.
func NewAROControlPlaneScope(ctx context.Context, params AROControlPlaneScopeParams) (*AROControlPlaneScope, error) {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "azure.aroControlPlaneScope.NewAROControlPlaneScope")
	defer done()

	if params.ControlPlane == nil {
		return nil, errors.New("failed to generate new scope from nil AROControlPlane")
	}

	credentialsProvider, err := NewAzureCredentialsProvider(ctx, params.CredentialCache, params.Client, params.ControlPlane.Spec.IdentityRef, params.ControlPlane.Namespace)
	if err != nil {
		return nil, errors.Wrap(err, "failed to init credentials provider")
	}
	err = params.AzureClients.setCredentialsWithProvider(ctx, params.ControlPlane.Spec.SubscriptionID, params.ControlPlane.Spec.AzureEnvironment, credentialsProvider)
	if err != nil {
		return nil, errors.Wrap(err, "failed to configure azure settings and credentials for Identity")
	}

	if params.Cache == nil {
		params.Cache = &AROControlPlaneCache{}
	}

	helper, err := patch.NewHelper(params.ControlPlane, params.Client)
	if err != nil {
		return nil, errors.Errorf("failed to init patch helper: %v", err)
	}

	scope := &AROControlPlaneScope{
		Client:          params.Client,
		AzureClients:    params.AzureClients,
		Cluster:         params.Cluster,
		ControlPlane:    params.ControlPlane,
		patchHelper:     helper,
		cache:           params.Cache,
		AsyncReconciler: params.Timeouts,
	}
	scope.initNetworkSpec()

	return scope, nil
}

// AROControlPlaneScope defines the basic context for an actuator to operate upon.
type AROControlPlaneScope struct {
	Client      client.Client
	patchHelper *patch.Helper
	cache       *AROControlPlaneCache

	AzureClients
	Cluster              *clusterv1.Cluster
	ControlPlane         *cplane.AROControlPlane
	ControlPlaneEndpoint clusterv1.APIEndpoint

	NetworkSpec *infrav1.NetworkSpec

	Kubeconfig                   *string
	KubeonfigExpirationTimestamp *time.Time

	// Key Vault related fields
	VaultName       *string
	VaultKeyName    *string
	VaultKeyVersion *string

	azure.AsyncReconciler
}

// SetAPIURL sets the API URL and visibility for the ARO control plane.
func (s *AROControlPlaneScope) SetAPIURL(url *string, _ *arohcp.Visibility) {
	if url != nil {
		s.ControlPlane.Status.APIURL = *url
	}
}

// SetKubeconfig sets the kubeconfig data and expiration timestamp.
func (s *AROControlPlaneScope) SetKubeconfig(kubeconfig *string, kubeconfigExpirationTimestamp *time.Time) {
	s.Kubeconfig = kubeconfig
	s.KubeonfigExpirationTimestamp = kubeconfigExpirationTimestamp
}

// GetAdminKubeconfigData returns the admin kubeconfig data as bytes.
func (s *AROControlPlaneScope) GetAdminKubeconfigData() []byte {
	if s.Kubeconfig == nil {
		return nil
	}
	return []byte(*s.Kubeconfig)
}

// MakeEmptyKubeConfigSecret creates an empty secret object that is used for storing kubeconfig secret data.
func (s *AROControlPlaneScope) MakeEmptyKubeConfigSecret() corev1.Secret {
	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secret.Name(s.Cluster.Name, secret.Kubeconfig),
			Namespace: s.Cluster.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(s.ControlPlane, infrav1.GroupVersion.WithKind(cplane.AROControlPlaneKind)),
			},
			Labels: map[string]string{clusterv1.ClusterNameLabel: s.Cluster.Name},
		},
	}
}

// SetStatusVersion sets the version profile in the control plane status.
func (s *AROControlPlaneScope) SetStatusVersion(version *arohcp.VersionProfile) {
	if version == nil {
		return
	}
	if version.ID != nil {
		s.ControlPlane.Status.Version = *version.ID
	}
}

// SetProvisioningState sets the provisioning state in the control plane status.
func (s *AROControlPlaneScope) SetProvisioningState(state *arohcp.ProvisioningState) {
	if state == nil {
		conditions.MarkUnknown(s.ControlPlane, cplane.AROControlPlaneReadyCondition, infrav1.CreatingReason, "nil ProvisioningState was returned")
		return
	}
	if *state == arohcp.ProvisioningStateSucceeded {
		conditions.MarkTrue(s.ControlPlane, cplane.AROControlPlaneReadyCondition)
		return
	}
	conditions.MarkFalse(s.ControlPlane, cplane.AROControlPlaneReadyCondition, infrav1.CreatingReason, clusterv1.ConditionSeverityInfo, "ProvisioningState=%s", string(*state))
}

// SetLongRunningOperationState will set the future on the AROControlPlane status to allow the resource to continue
// in the next reconciliation.
func (s *AROControlPlaneScope) SetLongRunningOperationState(future *infrav1.Future) {
	futures.Set(s.ControlPlane, future)
}

// GetLongRunningOperationState will get the future on the AROControlPlane status.
func (s *AROControlPlaneScope) GetLongRunningOperationState(name, service, futureType string) *infrav1.Future {
	return futures.Get(s.ControlPlane, name, service, futureType)
}

// DeleteLongRunningOperationState will delete the future from the AROControlPlane status.
func (s *AROControlPlaneScope) DeleteLongRunningOperationState(name, service, futureType string) {
	futures.Delete(s.ControlPlane, name, service, futureType)
}

// UpdateDeleteStatus updates a condition on the AROControlPlane status after a DELETE operation.
func (s *AROControlPlaneScope) UpdateDeleteStatus(condition clusterv1.ConditionType, service string, err error) {
	switch {
	case err == nil:
		conditions.MarkFalse(s.ControlPlane, condition, infrav1.DeletedReason, clusterv1.ConditionSeverityInfo, "%s successfully deleted", service)
	case azure.IsOperationNotDoneError(err):
		conditions.MarkFalse(s.ControlPlane, condition, infrav1.DeletingReason, clusterv1.ConditionSeverityInfo, "%s deleting", service)
	default:
		conditions.MarkFalse(s.ControlPlane, condition, infrav1.DeletionFailedReason, clusterv1.ConditionSeverityError, "%s failed to delete. err: %s", service, err.Error())
	}
}

// UpdatePutStatus updates a condition on the AROControlPlane status after a PUT operation.
func (s *AROControlPlaneScope) UpdatePutStatus(condition clusterv1.ConditionType, service string, err error) {
	switch {
	case err == nil:
		conditions.MarkTrue(s.ControlPlane, condition)
	case azure.IsOperationNotDoneError(err):
		conditions.MarkFalse(s.ControlPlane, condition, infrav1.CreatingReason, clusterv1.ConditionSeverityInfo, "%s creating or updating", service)
	default:
		conditions.MarkFalse(s.ControlPlane, condition, infrav1.FailedReason, clusterv1.ConditionSeverityError, "%s failed to create or update. err: %s", service, err.Error())
	}
}

// UpdatePatchStatus updates a condition on the AROControlPlane status after a PATCH operation.
func (s *AROControlPlaneScope) UpdatePatchStatus(condition clusterv1.ConditionType, service string, err error) {
	switch {
	case err == nil:
		conditions.MarkTrue(s.ControlPlane, condition)
	case azure.IsOperationNotDoneError(err):
		conditions.MarkFalse(s.ControlPlane, condition, infrav1.UpdatingReason, clusterv1.ConditionSeverityInfo, "%s updating", service)
	default:
		conditions.MarkFalse(s.ControlPlane, condition, infrav1.FailedReason, clusterv1.ConditionSeverityError, "%s failed to update. err: %s", service, err.Error())
	}
}

// HcpOpenShiftClusterSpecs returns the resource spec getter for HCP OpenShift clusters.
func (s *AROControlPlaneScope) HcpOpenShiftClusterSpecs(_ context.Context) azure.ResourceSpecGetter {
	ret := &hcpopenshiftclusters.HcpOpenShiftClustersSpec{
		Name:                   s.Cluster.Name,
		Location:               s.Location(),
		ResourceGroup:          s.ResourceGroup(),
		NodeResourceGroup:      s.NodeResourceGroup(),
		ManagedIdentities:      &s.ControlPlane.Spec.Platform.ManagedIdentities,
		AdditionalTags:         s.ControlPlane.Spec.AdditionalTags,
		NetworkSecurityGroupID: s.ControlPlane.Spec.Platform.NetworkSecurityGroupID,
		SubscriptionID:         s.ControlPlane.Spec.SubscriptionID,
		SubnetID:               s.ControlPlane.Spec.Platform.Subnet,
		VaultID:                s.ControlPlane.Spec.Platform.KeyVault,
		VaultName:              s.VaultName,
		VaultKeyName:           s.VaultKeyName,
		VaultKeyVersion:        s.VaultKeyVersion,
		VNetID:                 regexp.MustCompile("/subnets/.*").ReplaceAllLiteralString(s.ControlPlane.Spec.Platform.Subnet, ""),
		OutboundType:           s.ControlPlane.Spec.Platform.OutboundType,
		Network:                s.ControlPlane.Spec.Network,
		Version:                s.ControlPlane.Spec.Version,
		ChannelGroup:           s.ControlPlane.Spec.ChannelGroup,
		Visibility:             s.ControlPlane.Spec.Visibility,
	}
	return ret
}

// HcpOpenShiftClusterCredentialsSpecs returns the resource spec getter for HCP OpenShift cluster credentials.
func (s *AROControlPlaneScope) HcpOpenShiftClusterCredentialsSpecs(_ context.Context) azure.ResourceSpecGetter {
	ret := &hcpopenshiftclustercredentials.HcpOpenShiftClusterCredentialsSpec{
		Name:          s.Cluster.Name,
		ResourceGroup: s.ResourceGroup(),
		APIURI:        s.ControlPlane.Status.APIURL,
	}
	return ret
}

// AnnotateKubeconfigInvalid adds annotation aro.azure.com/kubeconfig-refresh-needed: true.
// This marks this secret as invalid.
func (s *AROControlPlaneScope) AnnotateKubeconfigInvalid(ctx context.Context) error {
	kubeconfigSecret := s.MakeEmptyKubeConfigSecret()
	key := client.ObjectKeyFromObject(&kubeconfigSecret)
	if err := s.Client.Get(ctx, key, &kubeconfigSecret); err != nil {
		// Secret doesn't exist - there is no need to invalidate it
		return nil //nolint:nilerr // returning nil when secret doesn't exist is intentional
	}
	// Update the kubeconfig secret
	kubeConfigSecret := s.MakeEmptyKubeConfigSecret()
	if _, err := controllerutil.CreateOrUpdate(ctx, s.Client, &kubeConfigSecret, func() error {
		// Add annotations for tracking
		if kubeConfigSecret.Annotations == nil {
			kubeConfigSecret.Annotations = make(map[string]string)
		}
		kubeConfigSecret.Annotations["aro.azure.com/kubeconfig-refresh-needed"] = kubeconfigRefreshNeededValue
		return nil
	}); err != nil {
		return errors.Wrap(err, "failed to invalidate kubeconfig secret")
	}
	return nil
}

// ShouldReconcileKubeconfig determines if kubeconfig needs reconciliation using metadata-based validation (Pattern 3).
// This avoids direct cluster connections and prevents issues with stale/invalid secrets.
func (s *AROControlPlaneScope) ShouldReconcileKubeconfig(ctx context.Context) bool {
	kubeconfigSecret := s.MakeEmptyKubeConfigSecret()
	key := client.ObjectKeyFromObject(&kubeconfigSecret)

	if err := s.Client.Get(ctx, key, &kubeconfigSecret); err != nil {
		// Secret doesn't exist - need to create it
		return true
	}

	// Check if kubeconfig data exists
	if len(kubeconfigSecret.Data[secret.KubeconfigDataName]) == 0 {
		return true
	}

	// Check for ARO-specific annotations that indicate refresh needed
	if kubeconfigSecret.Annotations != nil {
		if refreshNeeded, exists := kubeconfigSecret.Annotations["aro.azure.com/kubeconfig-refresh-needed"]; exists && refreshNeeded == kubeconfigRefreshNeededValue {
			return true
		}

		// Check if secret is older than configured threshold
		if lastUpdated, exists := kubeconfigSecret.Annotations["aro.azure.com/kubeconfig-last-updated"]; exists {
			lastUpdatedTime, err := time.Parse(time.RFC3339, lastUpdated)
			if err == nil {
				kubeconfigAge := time.Since(lastUpdatedTime)
				maxAge := s.GetKubeconfigMaxAge() // Configure based on ARO token lifetime
				if kubeconfigAge > maxAge {
					return true
				}
			}
		}
	}

	// Check if we have token expiration information and it's expired
	if s.KubeonfigExpirationTimestamp != nil {
		if time.Now().After(*s.KubeonfigExpirationTimestamp) {
			return true
		}
	}

	return false
}

// GetKubeconfigMaxAge returns the maximum age for kubeconfig before refresh is needed.
func (s *AROControlPlaneScope) GetKubeconfigMaxAge() time.Duration {
	// Default to 30 minutes, but could be made configurable via ControlPlane spec
	return 60 * time.Minute
}

// AROControlPlaneCache stores AROControlPlaneCache data locally so we don't have to hit the API multiple times within the same reconcile loop.
type AROControlPlaneCache struct {
	isVnetManaged *bool
}

// BaseURI returns the Azure ResourceManagerEndpoint.
func (s *AROControlPlaneScope) BaseURI() string {
	return s.ResourceManagerEndpoint
}

// GetClient returns the controller-runtime client.
func (s *AROControlPlaneScope) GetClient() client.Client {
	return s.Client
}

// GetDeletionTimestamp returns the deletion timestamp of the Cluster.
func (s *AROControlPlaneScope) GetDeletionTimestamp() *metav1.Time {
	return s.Cluster.DeletionTimestamp
}

// PatchObject persists the control plane configuration and status.
func (s *AROControlPlaneScope) PatchObject(ctx context.Context) error {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "scope.ManagedControlPlaneScope.PatchObject")
	defer done()

	conditions.SetSummary(s.ControlPlane)

	return s.patchHelper.Patch(
		ctx,
		s.ControlPlane,
		patch.WithOwnedConditions{Conditions: []clusterv1.ConditionType{
			clusterv1.ReadyCondition,
			cplane.AROControlPlaneReadyCondition,
			cplane.AROControlPlaneValidCondition,
			cplane.AROControlPlaneUpgradingCondition,
		}})
}

// Close closes the current scope persisting the control plane configuration and status.
func (s *AROControlPlaneScope) Close(ctx context.Context) error {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "scope.AROControlPlaneScope.Close")
	defer done()

	return s.PatchObject(ctx)
}

// Location returns location.
func (s *AROControlPlaneScope) Location() string {
	return s.ControlPlane.Spec.Platform.Location
}

// SetVersionStatus sets the k8s version in status.
func (s *AROControlPlaneScope) SetVersionStatus(version string) {
	s.ControlPlane.Status.Version = version
}

// MakeClusterCA returns a cluster CA Secret for the managed control plane.
func (s *AROControlPlaneScope) MakeClusterCA() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secret.Name(s.Cluster.Name, secret.ClusterCA),
			Namespace: s.Cluster.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(s.ControlPlane, cplane.GroupVersion.WithKind(cplane.AROControlPlaneKind)),
			},
		},
	}
}

// StoreClusterInfo stores the discovery cluster-info configmap in the kube-public namespace on the AKS cluster so kubeadm can access it to join nodes.
// This method now avoids direct cluster connections to prevent reliability issues with stale kubeconfigs.
func (s *AROControlPlaneScope) StoreClusterInfo(_ context.Context, _ []byte) error {
	// Skip cluster-info creation if we don't have a valid control plane endpoint
	// This avoids the need for remote cluster connections during kubeconfig reconciliation
	if s.ControlPlaneEndpoint.Host == "" || s.ControlPlaneEndpoint.Port == 0 {
		// Log that we're skipping this step but don't fail the reconciliation
		// The cluster-info will be created when the control plane is ready
		return nil
	}

	// For ARO clusters, we typically don't need to create cluster-info configmaps
	// as ARO manages this internally. This method is kept for compatibility
	// but we avoid remote connections to prevent kubeconfig validation issues.

	// For now, we skip this step to avoid the reliability issues with remote cluster connections

	return nil
}

// ASOOwner implements aso.Scope.
func (s *AROControlPlaneScope) ASOOwner() client.Object {
	return s.ControlPlane
}

// NetworkSecurityGroupSpecs returns the security group specs.
func (s *AROControlPlaneScope) NetworkSecurityGroupSpecs() []azure.ASOResourceSpecGetter[*asonetworkv1api20201101.NetworkSecurityGroup] {
	nsgspecs := make([]azure.ASOResourceSpecGetter[*asonetworkv1api20201101.NetworkSecurityGroup], len(s.NetworkSpec.Subnets))
	for i, subnet := range s.NetworkSpec.Subnets {
		nsgspecs[i] = &networksecutitygroup.NSGSpec{
			Name:                     subnet.SecurityGroup.Name,
			SecurityRules:            subnet.SecurityGroup.SecurityRules,
			ResourceGroup:            s.Vnet().ResourceGroup,
			Location:                 s.Location(),
			ClusterName:              s.ClusterName(),
			AdditionalTags:           s.AdditionalTags(),
			LastAppliedSecurityRules: s.getLastAppliedSecurityRules(subnet.SecurityGroup.Name),
		}
	}

	return nsgspecs
}

// SubnetSpecs returns the subnets specs.
func (s *AROControlPlaneScope) SubnetSpecs() []azure.ASOResourceSpecGetter[*asonetworkv1api20201101.VirtualNetworksSubnet] {
	numberOfSubnets := len(s.NetworkSpec.Subnets)

	subnetSpecs := make([]azure.ASOResourceSpecGetter[*asonetworkv1api20201101.VirtualNetworksSubnet], 0, numberOfSubnets)

	for _, subnet := range s.NetworkSpec.Subnets {
		subnetSpec := &subnets.SubnetSpec{
			Name:              subnet.Name,
			ResourceGroup:     s.ResourceGroup(),
			SubscriptionID:    s.SubscriptionID(),
			CIDRs:             subnet.CIDRBlocks,
			VNetName:          s.Vnet().Name,
			VNetResourceGroup: s.Vnet().ResourceGroup,
			IsVNetManaged:     s.IsVnetManaged(),
			RouteTableName:    subnet.RouteTable.Name,
			SecurityGroupName: subnet.SecurityGroup.Name,
			NatGatewayName:    subnet.NatGateway.Name,
			ServiceEndpoints:  subnet.ServiceEndpoints,
		}
		subnetSpecs = append(subnetSpecs, subnetSpec)
	}

	return subnetSpecs
}

// GroupSpecs returns the resource group spec.
func (s *AROControlPlaneScope) GroupSpecs() []azure.ASOResourceSpecGetter[*asoresourcesv1.ResourceGroup] {
	specs := []azure.ASOResourceSpecGetter[*asoresourcesv1.ResourceGroup]{
		&groups.GroupSpec{
			Name:           s.ResourceGroup(),
			AzureName:      s.ResourceGroup(),
			Location:       s.Location(),
			ClusterName:    s.ClusterName(),
			AdditionalTags: s.AdditionalTags(),
		},
	}
	if s.Vnet().ResourceGroup != "" && s.Vnet().ResourceGroup != s.ResourceGroup() {
		specs = append(specs, &groups.GroupSpec{
			Name:           azure.GetNormalizedKubernetesName(s.Vnet().ResourceGroup),
			AzureName:      s.Vnet().ResourceGroup,
			Location:       s.Location(),
			ClusterName:    s.ClusterName(),
			AdditionalTags: s.AdditionalTags(),
		})
	}
	return specs
}

// VNetSpec returns the virtual network spec.
func (s *AROControlPlaneScope) VNetSpec() azure.ASOResourceSpecGetter[*asonetworkv1api20201101.VirtualNetwork] {
	return &virtualnetworks.VNetSpec{
		ResourceGroup:    s.Vnet().ResourceGroup,
		Name:             s.Vnet().Name,
		CIDRs:            s.Vnet().CIDRBlocks,
		ExtendedLocation: s.ExtendedLocation(),
		Location:         s.Location(),
		ClusterName:      s.ClusterName(),
		AdditionalTags:   s.AdditionalTags(),
	}
}

// Vnet returns the cluster Vnet.
func (s *AROControlPlaneScope) Vnet() *infrav1.VnetSpec {
	return &s.NetworkSpec.Vnet
}

// Subnet returns the subnet with the provided name.
func (s *AROControlPlaneScope) Subnet(name string) infrav1.SubnetSpec {
	for _, sn := range s.NetworkSpec.Subnets {
		if sn.Name == name {
			return sn
		}
	}

	return infrav1.SubnetSpec{}
}

// SetSubnet sets the subnet spec for the subnet with the same name.
func (s *AROControlPlaneScope) SetSubnet(subnetSpec infrav1.SubnetSpec) {
	for i, sn := range s.NetworkSpec.Subnets {
		if sn.Name == subnetSpec.Name {
			s.NetworkSpec.Subnets[i] = subnetSpec
			return
		}
	}
}

// UpdateSubnetCIDRs updates the subnet CIDRs for the subnet with the same name.
func (s *AROControlPlaneScope) UpdateSubnetCIDRs(name string, cidrBlocks []string) {
	subnetSpecInfra := s.Subnet(name)
	subnetSpecInfra.CIDRBlocks = cidrBlocks
	s.SetSubnet(subnetSpecInfra)
}

// UpdateSubnetID updates the subnet ID for the subnet with the same name.
func (s *AROControlPlaneScope) UpdateSubnetID(name string, id string) {
	subnetSpecInfra := s.Subnet(name)
	subnetSpecInfra.ID = id
	s.SetSubnet(subnetSpecInfra)
}

// ResourceGroup returns the cluster resource group.
func (s *AROControlPlaneScope) ResourceGroup() string {
	return s.ControlPlane.Spec.Platform.ResourceGroup
}

// NodeResourceGroup returns the node resource group name for the ARO cluster.
func (s *AROControlPlaneScope) NodeResourceGroup() string {
	return s.ControlPlane.NodeResourceGroup()
}

// ClusterName returns the cluster name.
func (s *AROControlPlaneScope) ClusterName() string {
	return s.Cluster.Name
}

// Namespace returns the cluster namespace.
func (s *AROControlPlaneScope) Namespace() string {
	return s.Cluster.Namespace
}

// AdditionalTags returns AdditionalTags from the scope's AROControlPlane.
func (s *AROControlPlaneScope) AdditionalTags() infrav1.Tags {
	tags := make(infrav1.Tags)
	if s.ControlPlane.Spec.AdditionalTags != nil {
		tags = s.ControlPlane.Spec.AdditionalTags.DeepCopy()
	}
	return tags
}

// ExtendedLocation returns the extended location specification.
func (s *AROControlPlaneScope) ExtendedLocation() *infrav1.ExtendedLocationSpec {
	return nil
}

// IsVnetManaged returns whether the virtual network is managed.
func (s *AROControlPlaneScope) IsVnetManaged() bool {
	if s.cache.isVnetManaged != nil {
		return ptr.Deref(s.cache.isVnetManaged, false)
	}
	// TODO refactor `IsVnetManaged` so that it is able to use an upstream context
	// see https://github.com/kubernetes-sigs/cluster-api-provider-azure/issues/2581
	ctx := context.Background()
	ctx, log, done := tele.StartSpanWithLogger(ctx, "scope.ManagedControlPlaneScope.IsVnetManaged")
	defer done()

	vnet := s.VNetSpec().ResourceRef()
	vnet.SetNamespace(s.ASOOwner().GetNamespace())
	err := s.Client.Get(ctx, client.ObjectKeyFromObject(vnet), vnet)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return true
		}
		log.Error(err, "Unable to determine if AROControlPlaneScope VNET is managed by capz, assuming unmanaged", "AROCluster", s.ClusterName())
		return false
	}

	isManaged := infrav1.Tags(vnet.Status.Tags).HasOwned(s.ClusterName())
	s.cache.isVnetManaged = ptr.To(isManaged)
	return isManaged
}

func (s *AROControlPlaneScope) getLastAppliedSecurityRules(nsgName string) map[string]interface{} {
	// Retrieve the last applied security rules for all NSGs.
	lastAppliedSecurityRulesAll, err := s.AnnotationJSON(azure.SecurityRuleLastAppliedAnnotation)
	if err != nil {
		return map[string]interface{}{}
	}

	// Retrieve the last applied security rules for this NSG.
	lastAppliedSecurityRules, ok := lastAppliedSecurityRulesAll[nsgName].(map[string]interface{})
	if !ok {
		lastAppliedSecurityRules = map[string]interface{}{}
	}
	return lastAppliedSecurityRules
}

// AnnotationJSON returns a map[string]interface from a JSON annotation.
func (s *AROControlPlaneScope) AnnotationJSON(annotation string) (map[string]interface{}, error) {
	out := map[string]interface{}{}
	jsonAnnotation := s.ControlPlane.GetAnnotations()[annotation]
	if jsonAnnotation == "" {
		return out, nil
	}
	err := json.Unmarshal([]byte(jsonAnnotation), &out)
	if err != nil {
		return out, err
	}
	return out, nil
}

// UpdateAnnotationJSON updates the `annotation` with
// `content`. `content` in this case should be a `map[string]interface{}`
// suitable for turning into JSON. This `content` map will be marshalled into a
// JSON string before being set as the given `annotation`.
func (s *AROControlPlaneScope) UpdateAnnotationJSON(annotation string, content map[string]interface{}) error {
	b, err := json.Marshal(content)
	if err != nil {
		return err
	}
	s.SetAnnotation(annotation, string(b))
	return nil
}

// SetAnnotation sets a key value annotation on the ControlPlane.
func (s *AROControlPlaneScope) SetAnnotation(key, value string) {
	if s.ControlPlane.Annotations == nil {
		s.ControlPlane.Annotations = map[string]string{}
	}
	s.ControlPlane.Annotations[key] = value
}

func (s *AROControlPlaneScope) initNetworkSpec() {
	s.NetworkSpec = &infrav1.NetworkSpec{
		Vnet: infrav1.VnetSpec{
			ResourceGroup: s.ControlPlane.Spec.Platform.ResourceGroup,
			ID:            s.vnetID(),
			Name:          s.vnetName(),
			VnetClassSpec: infrav1.VnetClassSpec{
				CIDRBlocks: []string{"10.100.0.0/15"}, // TODO: mveber - add default value
			},
		},
		Subnets: infrav1.Subnets{
			infrav1.SubnetSpec{
				SubnetClassSpec: infrav1.SubnetClassSpec{
					Name:       s.subnetName(),
					CIDRBlocks: []string{"10.100.76.0/24"}, // TODO: mveber - add default value
				},
				ID: s.ControlPlane.Spec.Platform.Subnet,
				SecurityGroup: infrav1.SecurityGroup{
					ID:   s.ControlPlane.Spec.Platform.NetworkSecurityGroupID,
					Name: s.securityGroupName(),
				},
			},
		},
	}
}

func (s *AROControlPlaneScope) vnetID() string {
	// /subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/virtualNetworks/{vnetName}/subnets/{subnetName}
	re := regexp.MustCompile("(/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft.Network/virtualNetworks/[^/]+)/subnets/[^/]+")
	groups := re.FindStringSubmatch(s.ControlPlane.Spec.Platform.Subnet)
	if len(groups) <= 1 {
		return ""
	}
	return groups[1]
}

func (s *AROControlPlaneScope) vnetName() string {
	re := regexp.MustCompile("/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft.Network/virtualNetworks/([^/]+)/subnets/[^/]+")
	groups := re.FindStringSubmatch(s.ControlPlane.Spec.Platform.Subnet)
	if len(groups) <= 1 {
		return ""
	}
	return groups[1]
}
func (s *AROControlPlaneScope) subnetName() string {
	re := regexp.MustCompile("/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft.Network/virtualNetworks/[^/]+/subnets/([^/]+)")
	groups := re.FindStringSubmatch(s.ControlPlane.Spec.Platform.Subnet)
	if len(groups) <= 1 {
		return ""
	}
	return groups[1]
}

func (s *AROControlPlaneScope) securityGroupName() string {
	// /subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/networkSecurityGroups/{networkSecurityGroupName}
	re := regexp.MustCompile("/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft.Network/networkSecurityGroups/([^/]+)")
	groups := re.FindStringSubmatch(s.ControlPlane.Spec.Platform.NetworkSecurityGroupID)
	if len(groups) <= 1 {
		return ""
	}
	return groups[1]
}

// Name returns the cluster name for role assignment scope.
func (s *AROControlPlaneScope) Name() string {
	return s.ClusterName()
}

// CreateIfNotExists returns the true to create missing objects or false to raise an error if the required object is missing.
func (s *AROControlPlaneScope) CreateIfNotExists() bool {
	return s.ControlPlane.Spec.Platform.ManagedIdentities.CreateAROHCPManagedIdentities
}

// UserAssignedIdentitySpecs returns the user assigned identity specifications for ARO HCP cluster.
func (s *AROControlPlaneScope) UserAssignedIdentitySpecs() []azure.ASOResourceSpecGetter[*asomanagedidentityv1api20230131.UserAssignedIdentity] {
	var specs []azure.ASOResourceSpecGetter[*asomanagedidentityv1api20230131.UserAssignedIdentity]

	// Only create identities if CreateAROHCPManagedIdentities is true
	if !s.ControlPlane.Spec.Platform.ManagedIdentities.CreateAROHCPManagedIdentities {
		return specs
	}

	managedIdentities := s.ControlPlane.Spec.Platform.ManagedIdentities

	// Helper function to create and add identity spec
	createIdentitySpec := func(principalResourceID string) {
		// Extract ConfigMap information from managed identity resource ID
		identityName, configMapName, err := s.extractPrincipalIDConfigMapInfo(principalResourceID)
		if err != nil {
			return // Skip if we can't resolve the ConfigMap info
		}

		spec := &userassignedidentities.UserAssignedIdentitySpec{
			Name:          identityName,
			ConfigMapName: configMapName,
			ResourceGroup: s.ResourceGroup(),
			Location:      s.Location(),
			Tags:          convertTagsToStringPtr(s.AdditionalTags()),
		}

		specs = append(specs, spec)
	}

	// Service managed identity
	createIdentitySpec(managedIdentities.ServiceManagedIdentity)

	// Control plane operators
	if managedIdentities.ControlPlaneOperators != nil {
		controlPlaneOps := managedIdentities.ControlPlaneOperators
		createIdentitySpec(controlPlaneOps.ControlPlaneManagedIdentities)
		createIdentitySpec(controlPlaneOps.ClusterAPIAzureManagedIdentities)
		createIdentitySpec(controlPlaneOps.CloudControllerManagerManagedIdentities)
		createIdentitySpec(controlPlaneOps.IngressManagedIdentities)
		createIdentitySpec(controlPlaneOps.DiskCsiDriverManagedIdentities)
		createIdentitySpec(controlPlaneOps.FileCsiDriverManagedIdentities)
		createIdentitySpec(controlPlaneOps.ImageRegistryManagedIdentities)
		createIdentitySpec(controlPlaneOps.CloudNetworkConfigManagedIdentities)
		createIdentitySpec(controlPlaneOps.KmsManagedIdentities)
	}

	// Data plane operators
	if managedIdentities.DataPlaneOperators != nil {
		dataPlaneOps := managedIdentities.DataPlaneOperators
		createIdentitySpec(dataPlaneOps.DiskCsiDriverManagedIdentities)
		createIdentitySpec(dataPlaneOps.FileCsiDriverManagedIdentities)
		createIdentitySpec(dataPlaneOps.ImageRegistryManagedIdentities)
	}

	return specs
}

// convertTagsToStringPtr converts infrav1.Tags to map[string]*string for ASO compatibility.
func convertTagsToStringPtr(tags infrav1.Tags) map[string]*string {
	if tags == nil {
		return nil
	}
	result := make(map[string]*string)
	for k, v := range tags {
		result[k] = ptr.To(v)
	}
	return result
}

// KubernetesRoleAssignmentSpecs returns the Kubernetes role assignment specifications for ARO HCP cluster.
func (s *AROControlPlaneScope) KubernetesRoleAssignmentSpecs() []azure.ASOResourceSpecGetter[*asoauthorizationv1api20220401.RoleAssignment] {
	var specs []azure.ASOResourceSpecGetter[*asoauthorizationv1api20220401.RoleAssignment]

	// Get the HCP cluster spec
	ctx := context.Background()
	specResource := s.HcpOpenShiftClusterSpecs(ctx)
	spec, ok := specResource.(*hcpopenshiftclusters.HcpOpenShiftClustersSpec)
	if !ok {
		return specs
	}

	managedIdentities := spec.ManagedIdentities

	// Helper function to create and add role assignment spec
	createSpec := func(principalResourceID string, roleDef roleDEF, scope, suffix string) {
		if principalResourceID == "" || scope == "" {
			return // Skip if invalid parameters
		}

		// Extract ConfigMap information from managed identity resource ID
		identityName, configMapName, err := s.extractPrincipalIDConfigMapInfo(principalResourceID)
		if err != nil {
			return // Skip if we can't resolve the ConfigMap info
		}

		// Parse owner information from scope
		ownerName, ownerGroup, ownerKind, err := roleassignmentsaso.ParseOwnerFromScope(scope)
		if err != nil {
			return // Skip if we can't parse the scope
		}

		roleSpec := &roleassignmentsaso.KubernetesRoleAssignmentSpec{
			Name:                     fmt.Sprintf("%s-%s", identityName, suffix),
			Namespace:                s.ASOOwner().GetNamespace(),
			PrincipalIDConfigMapName: configMapName,
			PrincipalIDConfigMapKey:  "principalId",
			PrincipalType:            "ServicePrincipal", // User-assigned managed identities are service principals
			RoleDefinitionReference:  fmt.Sprintf("/subscriptions/%s/providers/Microsoft.Authorization/roleDefinitions/%s", s.SubscriptionID(), string(roleDef)),
			OwnerName:                ownerName,
			OwnerGroup:               ownerGroup,
			OwnerKind:                ownerKind,
			ClusterName:              s.ClusterName(),
			Tags:                     s.AdditionalTags(),
		}

		specs = append(specs, roleSpec)
	}

	// Skip role assignments if managed identities structure is incomplete
	if managedIdentities.ControlPlaneOperators == nil || managedIdentities.DataPlaneOperators == nil {
		return specs
	}

	// ClusterAPI Azure managed identity has HCP Cluster API Provider role on subnet
	createSpec(managedIdentities.ControlPlaneOperators.ClusterAPIAzureManagedIdentities, roleDEFhcpClusterAPIProvider, spec.SubnetID, "hcpclusterapiproviderroleid-subnet")
	// Service managed identity has Reader role on ClusterAPI Azure managed identity
	createSpec(managedIdentities.ServiceManagedIdentity, roleDEFreader, managedIdentities.ControlPlaneOperators.ClusterAPIAzureManagedIdentities, "readerroleid-clusterapiazuremi")
	// Control Plane managed identity has HCP Control Plane Operator role on VNet
	createSpec(managedIdentities.ControlPlaneOperators.ControlPlaneManagedIdentities, roleDEFhcpControlPlaneOperator, spec.VNetID, "hcpcontrolplaneoperatorroleid-vnet")
	// Control Plane managed identity has HCP Control Plane Operator role on Network Security Group
	createSpec(managedIdentities.ControlPlaneOperators.ControlPlaneManagedIdentities, roleDEFhcpControlPlaneOperator, spec.NetworkSecurityGroupID, "hcpcontrolplaneoperatorroleid-nsg")
	// Service managed identity has Reader role on Control Plane managed identity
	createSpec(managedIdentities.ServiceManagedIdentity, roleDEFreader, managedIdentities.ControlPlaneOperators.ControlPlaneManagedIdentities, "readerroleid-controlplanemi")
	// Cloud Controller Manager managed identity has Cloud Controller Manager role on subnet
	createSpec(managedIdentities.ControlPlaneOperators.CloudControllerManagerManagedIdentities, roleDEFcloudControllerManager, spec.SubnetID, "cloudcontrollermanagerroleid-subnet")
	// Cloud Controller Manager managed identity has Cloud Controller Manager role on Network Security Group
	createSpec(managedIdentities.ControlPlaneOperators.CloudControllerManagerManagedIdentities, roleDEFcloudControllerManager, spec.NetworkSecurityGroupID, "cloudcontrollermanagerroleid-nsg")
	// Service managed identity has Reader role on Cloud Controller Manager managed identity
	createSpec(managedIdentities.ServiceManagedIdentity, roleDEFreader, managedIdentities.ControlPlaneOperators.CloudControllerManagerManagedIdentities, "readerroleid-cloudcontrollermanagermi")
	// Ingress managed identity has Ingress Operator role on subnet
	createSpec(managedIdentities.ControlPlaneOperators.IngressManagedIdentities, roleDEFingressOperator, spec.SubnetID, "ingressoperatorroleid-subnet")
	// Service managed identity has Reader role on Ingress managed identity
	createSpec(managedIdentities.ServiceManagedIdentity, roleDEFreader, managedIdentities.ControlPlaneOperators.IngressManagedIdentities, "readerroleid-ingressmi")
	// Service managed identity has Reader role on Disk CSI Driver managed identity
	createSpec(managedIdentities.ServiceManagedIdentity, roleDEFreader, managedIdentities.ControlPlaneOperators.DiskCsiDriverManagedIdentities, "readerroleid-diskcsidrivermi")
	// File CSI Driver managed identity has File Storage Operator role on subnet
	createSpec(managedIdentities.ControlPlaneOperators.FileCsiDriverManagedIdentities, roleDEFfileStorageOperator, spec.SubnetID, "filestorageoperatorroleid-subnet")
	// File CSI Driver managed identity has File Storage Operator role on Network Security Group
	createSpec(managedIdentities.ControlPlaneOperators.FileCsiDriverManagedIdentities, roleDEFfileStorageOperator, spec.NetworkSecurityGroupID, "filestorageoperatorroleid-nsg")
	// Service managed identity has Reader role on File CSI Driver managed identity
	createSpec(managedIdentities.ServiceManagedIdentity, roleDEFreader, managedIdentities.ControlPlaneOperators.FileCsiDriverManagedIdentities, "readerroleid-filecsidrivermi")
	// Service managed identity has Reader role on Image Registry managed identity
	createSpec(managedIdentities.ServiceManagedIdentity, roleDEFreader, managedIdentities.ControlPlaneOperators.ImageRegistryManagedIdentities, "readerroleid-imageregistrymi")
	// Cloud Network Config managed identity has Network Operator role on subnet
	createSpec(managedIdentities.ControlPlaneOperators.CloudNetworkConfigManagedIdentities, roleDEFnetworkOperator, spec.SubnetID, "networkoperatorroleid-subnet")
	// Cloud Network Config managed identity has Network Operator role on VNet
	createSpec(managedIdentities.ControlPlaneOperators.CloudNetworkConfigManagedIdentities, roleDEFnetworkOperator, spec.VNetID, "networkoperatorroleid-vnet")
	// Service managed identity has Reader role on Cloud Network Config managed identity
	createSpec(managedIdentities.ServiceManagedIdentity, roleDEFreader, managedIdentities.ControlPlaneOperators.CloudNetworkConfigManagedIdentities, "readerroleid-cloudnetworkconfigmi")
	if spec.VaultID != "" {
		// Service managed identity has Reader role on KMS managed identity
		createSpec(managedIdentities.ServiceManagedIdentity, roleDEFreader, managedIdentities.ControlPlaneOperators.KmsManagedIdentities, "readerroleid-kmsmi")
		// KMS managed identity has Key Vault Crypto Service Encryption User role on KeyVault
		createSpec(managedIdentities.ControlPlaneOperators.KmsManagedIdentities, roleDEFkeyVaultCryptoUserRoleID, spec.VaultID, "keyvaultcryptouserroleid-keyvault")
	}
	// Service managed identity has Federated Credentials role on Data Plane Disk CSI Driver managed identity
	createSpec(managedIdentities.ServiceManagedIdentity, roleDEFfederatedCredentials, managedIdentities.DataPlaneOperators.DiskCsiDriverManagedIdentities, "federatedcredentialsroleid-dpdiskcsidrivermi")
	// Service managed identity has Federated Credentials role on Data Plane File CSI Driver managed identity
	createSpec(managedIdentities.ServiceManagedIdentity, roleDEFfederatedCredentials, managedIdentities.DataPlaneOperators.FileCsiDriverManagedIdentities, "federatedcredentialsroleid-dpfilecsidrivermi")
	// Data Plane File CSI Driver managed identity has File Storage Operator role on subnet
	createSpec(managedIdentities.DataPlaneOperators.FileCsiDriverManagedIdentities, roleDEFfileStorageOperator, spec.SubnetID, "filestorageoperatorroleid-subnet")
	// Data Plane File CSI Driver managed identity has File Storage Operator role on Network Security Group
	createSpec(managedIdentities.DataPlaneOperators.FileCsiDriverManagedIdentities, roleDEFfileStorageOperator, spec.NetworkSecurityGroupID, "filestorageoperatorroleid-nsg")
	// Service managed identity has Federated Credentials role on Data Plane Image Registry managed identity
	createSpec(managedIdentities.ServiceManagedIdentity, roleDEFfederatedCredentials, managedIdentities.DataPlaneOperators.ImageRegistryManagedIdentities, "federatedcredentialsroleid-dpimageregistrymi")
	// Service managed identity has HCP Service Managed Identity role on VNet
	createSpec(managedIdentities.ServiceManagedIdentity, roleDEFhcpServiceManagedIdentity, spec.VNetID, "hcpservicemanagedidentityroleid-vnet")
	// Service managed identity has HCP Service Managed Identity role on subnet
	createSpec(managedIdentities.ServiceManagedIdentity, roleDEFhcpServiceManagedIdentity, spec.SubnetID, "hcpservicemanagedidentityroleid-subnet")
	// Service managed identity has HCP Service Managed Identity role on Network Security Group
	createSpec(managedIdentities.ServiceManagedIdentity, roleDEFhcpServiceManagedIdentity, spec.NetworkSecurityGroupID, "hcpservicemanagedidentityroleid-nsg")

	return specs
}

// extractPrincipalIDConfigMapInfo extracts the identityNAme, ConfigMap name and key for the principal ID
// by parsing the user assigned identity resource ID.
func (s *AROControlPlaneScope) extractPrincipalIDConfigMapInfo(resourceID string) (identityName, configMapName string, err error) {
	// Parse resource ID to extract identity name
	spec, err := userassignedidentities.ParseUserAssignedIdentityResourceID(resourceID)
	if err != nil {
		return "", "", errors.Wrap(err, "failed to parse user assigned identity resource ID")
	}

	// The ConfigMap name follows the pattern: identity-map-{identity-name}
	// This matches what ASO creates for UserAssignedIdentity resources
	identityName = spec.ResourceName()
	configMapName = fmt.Sprintf("identity-map-%s", identityName)

	return identityName, configMapName, nil
}

// KeyVaultSpecs returns the Key Vault specs.
func (s *AROControlPlaneScope) KeyVaultSpecs() []azure.ResourceSpecGetter {
	if s.ControlPlane.Spec.Platform.KeyVault == "" {
		return []azure.ResourceSpecGetter{}
	}

	// Extract vault name from the Key Vault resource ID
	vaultName, err := extractVaultNameFromResourceID(s.ControlPlane.Spec.Platform.KeyVault)
	if err != nil {
		// Return empty specs if we can't extract the vault name
		return []azure.ResourceSpecGetter{}
	}

	return []azure.ResourceSpecGetter{
		&keyvaults.KeyVaultSpec{
			Name:           vaultName,
			ResourceGroup:  s.ResourceGroup(),
			Location:       s.Location(),
			TenantID:       s.TenantID(),
			SKU:            armkeyvault.SKUNameStandard,
			AccessPolicies: []*armkeyvault.AccessPolicyEntry{},
			Tags:           convertTagsToStringPtr(s.AdditionalTags()),
		},
	}
}

// VaultSpecs returns the Key Vault specs.
func (s *AROControlPlaneScope) VaultSpecs() []azure.ASOResourceSpecGetter[*v1api20230701.Vault] {
	var specs []azure.ASOResourceSpecGetter[*v1api20230701.Vault]
	if s.ControlPlane.Spec.Platform.KeyVault == "" {
		return specs
	}

	// Extract vault name from the Key Vault resource ID
	vaultName, err := extractVaultNameFromResourceID(s.ControlPlane.Spec.Platform.KeyVault)
	if err == nil {
		s := &vaults.VaultSpec{
			Name:          vaultName,
			ResourceGroup: s.ResourceGroup(),
			Location:      s.Location(),
			TenantID:      s.TenantID(),
			Tags:          s.AdditionalTags(),
		}
		specs = append(specs, s)
	}
	return specs
}

// extractVaultNameFromResourceID extracts the vault name from a Key Vault resource ID.
func extractVaultNameFromResourceID(resourceID string) (string, error) {
	parts := strings.Split(resourceID, "/")
	for i, part := range parts {
		if part == keyvaults.VaultsResourceType && i+1 < len(parts) {
			return parts[i+1], nil
		}
	}
	return "", fmt.Errorf("could not extract vault name from resource ID: %s", resourceID)
}

// GetKeyVaultResourceID returns the Key Vault resource ID from the platform spec.
func (s *AROControlPlaneScope) GetKeyVaultResourceID() string {
	return s.ControlPlane.Spec.Platform.KeyVault
}

// SetVaultInfo sets the vault information in the scope.
func (s *AROControlPlaneScope) SetVaultInfo(vaultName, keyName, keyVersion *string) {
	s.VaultName = vaultName
	s.VaultKeyName = keyName
	s.VaultKeyVersion = keyVersion
}
