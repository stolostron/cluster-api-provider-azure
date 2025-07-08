/*
Copyright 2018 The Kubernetes Authors.

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
	asonetworkv1api20201101 "github.com/Azure/azure-service-operator/v2/api/network/v1api20201101"
	asoresourcesv1 "github.com/Azure/azure-service-operator/v2/api/resources/v1api20200601"
	arohcp "github.com/marek-veber/ARO-HCP/external/api/v20240610preview/generated"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	bootstrapapi "k8s.io/cluster-bootstrap/token/api"
	"k8s.io/utils/ptr"
	"regexp"
	infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	"sigs.k8s.io/cluster-api-provider-azure/azure"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/groups"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/hcpopenshiftclustercredentials"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/hcpopenshiftclusters"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/securitygroups"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/subnets"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/virtualnetworks"
	cplane "sigs.k8s.io/cluster-api-provider-azure/exp/api/controlplane/v1beta2"
	"sigs.k8s.io/cluster-api-provider-azure/util/futures"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/controllers/remote"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/secret"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"time"
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
	// TODO: mveber - fill this
	SubscriptionID   string
	AzureEnvironment string
}

// NewAROControlPlaneScope creates a new Scope from the supplied parameters.
// This is meant to be called for each reconcile iteration.
func NewAROControlPlaneScope(ctx context.Context, params AROControlPlaneScopeParams) (*AROControlPlaneScope, error) {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "azure.aroControlPlaneScope.NewAROControlPlaneScope")
	defer done()

	/*
		TODO: mveber - how to pause/delete if Cluster == nil
		if params.Cluster == nil {
			return nil, errors.New("failed to generate new scope from nil Cluster")
		}
	*/
	if params.ControlPlane == nil {
		return nil, errors.New("failed to generate new scope from nil AROControlPlane")
	}

	credentialsProvider, err := NewAzureCredentialsProvider(ctx, params.CredentialCache, params.Client, params.ControlPlane.Spec.IdentityRef, params.ControlPlane.Namespace)
	if err != nil {
		return nil, errors.Wrap(err, "failed to init credentials provider")
	}
	err = params.AzureClients.setCredentialsWithProvider(ctx, params.SubscriptionID, params.AzureEnvironment, credentialsProvider)
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
	azure.AsyncReconciler
}

func (s *AROControlPlaneScope) SetApiUrl(url *string, visibility *arohcp.Visibility) {
	if url != nil {
		s.ControlPlane.Status.APIURL = *url
	}
}

func (s *AROControlPlaneScope) SetKubeconfig(kubeconfig *string, kubeconfigExpirationTimestamp *time.Time) {
	s.Kubeconfig = kubeconfig
	s.KubeonfigExpirationTimestamp = kubeconfigExpirationTimestamp
}

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

func (s *AROControlPlaneScope) SetStatusVersion(version *arohcp.VersionProfile) {
	s.ControlPlane.Status.Version = *version.ID
}

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

func (s *AROControlPlaneScope) HcpOpenShiftClusterSpecs(ctx context.Context) azure.ResourceSpecGetter {
	ret := &hcpopenshiftclusters.HcpOpenShiftClustersSpec{
		Name:                   s.Cluster.Name,
		Location:               s.Location(),
		ResourceGroup:          s.ResourceGroup(),
		ManagedIdentities:      &s.ControlPlane.Spec.Platform.ManagedIdentities,
		AdditionalTags:         s.ControlPlane.Spec.AdditionalTags,
		NetworkSecurityGroupID: s.ControlPlane.Spec.Platform.NetworkSecurityGroupID,
		Subnet:                 s.ControlPlane.Spec.Platform.Subnet,
		OutboundType:           s.ControlPlane.Spec.Platform.OutboundType,
		Network:                s.ControlPlane.Spec.Network,
		Version:                s.ControlPlane.Spec.Version,
		ChannelGroup:           s.ControlPlane.Spec.ChannelGroup,
		Visibility:             s.ControlPlane.Spec.Visibility,
	}
	return ret
}

func (s *AROControlPlaneScope) HcpOpenShiftClusterCredentialsSpecs(ctx context.Context) azure.ResourceSpecGetter {
	ret := &hcpopenshiftclustercredentials.HcpOpenShiftClusterCredentialsSpec{
		Name:               s.Cluster.Name,
		ResourceGroup:      s.ResourceGroup(),
		APIURI:             s.ControlPlane.Status.APIURL,
		HasValidKubeconfig: s.HasValidKubeconfig(ctx),
	}
	return ret
}

func (s *AROControlPlaneScope) HasValidKubeconfig(ctx context.Context) bool {
	obj := s.MakeEmptyKubeConfigSecret()
	key := client.ObjectKeyFromObject(&obj)
	if err := s.Client.Get(ctx, key, &obj); err != nil {
		// eny error includes not found
		return false
	}

	remoteClient, err := remote.NewClusterClient(ctx, managedControlPlaneScopeName, s.Client, types.NamespacedName{
		Namespace: s.Cluster.Namespace,
		Name:      s.Cluster.Name,
	})
	if err != nil {
		return false
	}

	// kube-public configmap kube-root-ca.crt
	obj2 := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-root-ca.crt",
			Namespace: metav1.NamespacePublic,
		},
	}
	key2 := client.ObjectKeyFromObject(obj2)
	if err := remoteClient.Get(ctx, key2, obj2); err != nil {
		return false
	}

	return true
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

// Location returns location
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
				*metav1.NewControllerRef(s.ControlPlane, infrav1.GroupVersion.WithKind(infrav1.AzureManagedControlPlaneKind)),
			},
		},
	}
}

// StoreClusterInfo stores the discovery cluster-info configmap in the kube-public namespace on the AKS cluster so kubeadm can access it to join nodes.
func (s *AROControlPlaneScope) StoreClusterInfo(ctx context.Context, caData []byte) error {
	remoteclient, err := remote.NewClusterClient(ctx, managedControlPlaneScopeName, s.Client, types.NamespacedName{
		Namespace: s.Cluster.Namespace,
		Name:      s.Cluster.Name,
	})
	if err != nil {
		return errors.Wrap(err, "failed to create remote cluster kubeclient")
	}
	/*
		// kube-public configmap kube-root-ca.crt
		obj := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kube-root-ca.crt",
				Namespace: metav1.NamespacePublic,
			},
		}
		key := client.ObjectKeyFromObject(obj)
		if err := remoteclient.Get(ctx, key, obj); err != nil {
			return errors.Wrap(err, "failed to GET OBJECT FROM remote cluster kubeclient")
		}
	*/

	discoveryFile := clientcmdapi.NewConfig()
	discoveryFile.Clusters[""] = &clientcmdapi.Cluster{
		CertificateAuthorityData: caData,
		Server: fmt.Sprintf(
			"%s:%d",
			s.ControlPlaneEndpoint.Host,
			s.ControlPlaneEndpoint.Port,
		),
	}

	data, err := yaml.Marshal(&discoveryFile)
	if err != nil {
		return errors.Wrap(err, "failed to serialize cluster-info to yaml")
	}

	clusterInfo := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      bootstrapapi.ConfigMapClusterInfo,
			Namespace: metav1.NamespacePublic,
		},
		Data: map[string]string{
			bootstrapapi.KubeConfigKey: string(data),
		},
	}

	if _, err := controllerutil.CreateOrUpdate(ctx, remoteclient, clusterInfo, func() error {
		clusterInfo.Data[bootstrapapi.KubeConfigKey] = string(data)
		return nil
	}); err != nil {
		return errors.Wrapf(err, "failed to reconcile certificate authority data secret for cluster")
	}

	return nil
}

// ASOOwner implements aso.Scope.
func (s *AROControlPlaneScope) ASOOwner() client.Object {
	return s.ControlPlane
}

// NSGSpecs returns the security group specs.
func (s *AROControlPlaneScope) NSGSpecs() []azure.ResourceSpecGetter {
	nsgspecs := make([]azure.ResourceSpecGetter, len(s.NetworkSpec.Subnets))
	for i, subnet := range s.NetworkSpec.Subnets {
		nsgspecs[i] = &securitygroups.NSGSpec{
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

func (s *AROControlPlaneScope) ExtendedLocation() *infrav1.ExtendedLocationSpec {
	return nil
}

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
		log.Error(err, "Unable to determine if ManagedControlPlaneScope VNET is managed by capz, assuming unmanaged", "AzureManagedCluster", s.ClusterName())
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
			ID:            s.vnetId(),
			Name:          s.vnetName(),
		},
		Subnets: infrav1.Subnets{
			infrav1.SubnetSpec{
				SubnetClassSpec: infrav1.SubnetClassSpec{
					Name: s.subnetName(),
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

func (s *AROControlPlaneScope) vnetId() string {
	// /subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/virtualNetworks/{vnetName}/subnets/{subnetName}
	re := regexp.MustCompile("(/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft.Network/virtualNetworks/[^/]+)/subnets/[^/]+")
	groups := re.FindStringSubmatch(s.ControlPlane.Spec.Platform.Subnet)
	if groups == nil && len(groups) > 0 {
		return ""
	}
	return groups[1]
}

func (s *AROControlPlaneScope) vnetName() string {
	re := regexp.MustCompile("/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft.Network/virtualNetworks/([^/]+)/subnets/[^/]+")
	groups := re.FindStringSubmatch(s.ControlPlane.Spec.Platform.Subnet)
	if groups == nil && len(groups) > 0 {
		return ""
	}
	return groups[1]

}
func (s *AROControlPlaneScope) subnetName() string {
	re := regexp.MustCompile("/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft.Network/virtualNetworks/[^/]+/subnets/([^/]+)")
	groups := re.FindStringSubmatch(s.ControlPlane.Spec.Platform.Subnet)
	if groups == nil && len(groups) > 0 {
		return ""
	}
	return groups[1]
}

func (s *AROControlPlaneScope) securityGroupName() string {
	// /subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Network/networkSecurityGroups/{networkSecurityGroupName}
	re := regexp.MustCompile("/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft.Network/networkSecurityGroups/([^/]+)")
	groups := re.FindStringSubmatch(s.ControlPlane.Spec.Platform.NetworkSecurityGroupID)
	if groups == nil && len(groups) > 0 {
		return ""
	}
	return groups[1]
}
