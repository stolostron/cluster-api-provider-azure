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

package hcpopenshiftclusters

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"k8s.io/utils/ptr"

	cplane "sigs.k8s.io/cluster-api-provider-azure/exp/api/controlplane/v1beta2"
	"sigs.k8s.io/cluster-api-provider-azure/exp/api/v1beta2"
	arohcp "sigs.k8s.io/cluster-api-provider-azure/exp/third_party/aro-hcp/api/v20240610preview/generated"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
)

// HcpOpenShiftClustersSpec defines the specification for a HcpOpenShiftCluster.
type HcpOpenShiftClustersSpec struct {
	Name                   string
	Location               string
	ResourceGroup          string
	NodeResourceGroup      string
	ManagedIdentities      *cplane.ManagedIdentities
	AdditionalTags         map[string]string
	SubscriptionID         string
	NetworkSecurityGroupID string
	SubnetID               string
	VNetID                 string
	VaultID                string
	VaultName              *string
	VaultKeyName           *string
	VaultKeyVersion        *string
	OutboundType           string
	Network                *cplane.NetworkSpec
	Version                string
	ChannelGroup           v1beta2.ChannelGroupType
	Visibility             string
}

// ResourceName returns the name of the HcpOpenShiftCluster.
func (s *HcpOpenShiftClustersSpec) ResourceName() string {
	return s.Name
}

// ResourceGroupName returns the name of the resource group.
func (s *HcpOpenShiftClustersSpec) ResourceGroupName() string {
	return s.ResourceGroup
}

// OwnerResourceName returns the cluster name.
func (s *HcpOpenShiftClustersSpec) OwnerResourceName() string {
	return s.Name
}

// GetManagedIdentities converts managed identities.
func (s *HcpOpenShiftClustersSpec) GetManagedIdentities() (*arohcp.UserAssignedIdentitiesProfile, *arohcp.ManagedServiceIdentity) {
	managedServiceIdentityType := arohcp.ManagedServiceIdentityTypeUserAssigned
	userAssignedIdentities := &arohcp.UserAssignedIdentitiesProfile{
		ControlPlaneOperators: map[string]*string{
			"control-plane":            &s.ManagedIdentities.ControlPlaneOperators.ControlPlaneManagedIdentities,
			"cluster-api-azure":        &s.ManagedIdentities.ControlPlaneOperators.ClusterAPIAzureManagedIdentities,
			"cloud-controller-manager": &s.ManagedIdentities.ControlPlaneOperators.CloudControllerManagerManagedIdentities,
			"ingress":                  &s.ManagedIdentities.ControlPlaneOperators.IngressManagedIdentities,
			"disk-csi-driver":          &s.ManagedIdentities.ControlPlaneOperators.DiskCsiDriverManagedIdentities,
			"file-csi-driver":          &s.ManagedIdentities.ControlPlaneOperators.FileCsiDriverManagedIdentities,
			"image-registry":           &s.ManagedIdentities.ControlPlaneOperators.ImageRegistryManagedIdentities,
			"cloud-network-config":     &s.ManagedIdentities.ControlPlaneOperators.CloudNetworkConfigManagedIdentities,
			"kms":                      &s.ManagedIdentities.ControlPlaneOperators.KmsManagedIdentities,
		},
		DataPlaneOperators: map[string]*string{
			"disk-csi-driver": &s.ManagedIdentities.DataPlaneOperators.DiskCsiDriverManagedIdentities,
			"file-csi-driver": &s.ManagedIdentities.DataPlaneOperators.FileCsiDriverManagedIdentities,
			"image-registry":  &s.ManagedIdentities.DataPlaneOperators.ImageRegistryManagedIdentities,
		},
		ServiceManagedIdentity: &s.ManagedIdentities.ServiceManagedIdentity,
	}
	if s.VaultID == "" {
		delete(userAssignedIdentities.ControlPlaneOperators, "kms")
	}
	managedServiceIdentity := &arohcp.ManagedServiceIdentity{
		Type:                   &managedServiceIdentityType,
		UserAssignedIdentities: map[string]*arohcp.UserAssignedIdentity{},
		// PrincipalID:            nil,
		// TenantID:               nil,
	}
	midsMap := []map[string]*string{
		userAssignedIdentities.ControlPlaneOperators,
		// userAssignedIdentities.DataPlaneOperators, // we should skip DataPlaneOperators here
		{"": userAssignedIdentities.ServiceManagedIdentity},
	}
	for _, midMap := range midsMap {
		for _, mid := range midMap {
			if mid == nil || *mid == "" {
				continue
			}
			managedServiceIdentity.UserAssignedIdentities[*mid] = &arohcp.UserAssignedIdentity{}
		}
	}
	return userAssignedIdentities, managedServiceIdentity
}

// getTags converts AdditionalTags to the required format.
func (s *HcpOpenShiftClustersSpec) getTags() map[string]*string {
	ret := map[string]*string{}
	for k, v := range s.AdditionalTags {
		ret[k] = &v
	}
	return ret
}

// getManagedResourceGroup returns the managed resource group name.
func (s *HcpOpenShiftClustersSpec) getManagedResourceGroup() *string {
	return &s.NodeResourceGroup
}

// getOutboundType returns the outbound type for the cluster.
func (s *HcpOpenShiftClustersSpec) getOutboundType() (*arohcp.OutboundType, error) {
	if s.OutboundType == "LoadBalancer" {
		outboundType := arohcp.OutboundTypeLoadBalancer
		return &outboundType, nil
	}
	return nil, errors.Errorf("unsupported outbound type %s", s.OutboundType)
}

// getNetworkType returns the network type for the cluster.
func (s *HcpOpenShiftClustersSpec) getNetworkType() (*arohcp.NetworkType, error) {
	if s.Network.NetworkType == "OVNKubernetes" {
		networkType := arohcp.NetworkTypeOVNKubernetes
		return &networkType, nil
	}
	if s.Network.NetworkType == "Other" {
		networkType := arohcp.NetworkTypeOther
		return &networkType, nil
	}
	return nil, errors.Errorf("unsupported network type %s", s.Network.NetworkType)
}

// getVisibility returns the visibility type for the cluster.
func (s *HcpOpenShiftClustersSpec) getVisibility() (*arohcp.Visibility, error) {
	if s.Visibility == "Private" {
		visibility := arohcp.VisibilityPrivate
		return &visibility, nil
	}
	if s.Visibility == "Public" {
		visibility := arohcp.VisibilityPublic
		return &visibility, nil
	}
	return nil, errors.Errorf("unsupported visibility type %s", s.Visibility)
}

// Parameters returns the parameters for the HcpOpenShiftCluster.
func (s *HcpOpenShiftClustersSpec) Parameters(ctx context.Context, existing interface{}) (params interface{}, err error) {
	_, log, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftclusters.Parameters")
	defer done()

	var existingHcpOpenShiftCluster *arohcp.HcpOpenShiftCluster
	if existing != nil {
		hcpOpenShiftCluster, ok := existing.(arohcp.HcpOpenShiftCluster)
		if !ok {
			return nil, errors.Errorf("%T is not a arohcp.HcpOpenShiftCluster", existing)
		}
		existingHcpOpenShiftCluster = &hcpOpenShiftCluster
	}

	userAssignedIdentities, managedServiceIdentity := s.GetManagedIdentities()
	outboundType, errO := s.getOutboundType()
	if errO != nil {
		return nil, errO
	}

	networkType, errN := s.getNetworkType()
	if errN != nil {
		return nil, errN
	}

	visibility, errV := s.getVisibility()
	if errV != nil {
		return nil, errV
	}
	dataEncryption := &arohcp.EtcdDataEncryptionProfile{}
	if s.VaultID != "" {
		dataEncryption.KeyManagementMode = ptr.To(arohcp.EtcdDataEncryptionKeyManagementModeTypeCustomerManaged)
		dataEncryption.CustomerManaged = &arohcp.CustomerManagedEncryptionProfile{
			EncryptionType: ptr.To(arohcp.CustomerManagedEncryptionTypeKms),
			Kms: &arohcp.KmsEncryptionProfile{
				ActiveKey: &arohcp.KmsKey{
					VaultName: s.VaultName,
					Name:      s.VaultKeyName,
					Version:   s.VaultKeyVersion,
				},
			},
		}
	}

	ret := arohcp.HcpOpenShiftCluster{
		Location: ptr.To(s.Location),
		Identity: managedServiceIdentity,
		Properties: &arohcp.HcpOpenShiftClusterProperties{
			Platform: &arohcp.PlatformProfile{
				NetworkSecurityGroupID: &s.NetworkSecurityGroupID,
				OperatorsAuthentication: &arohcp.OperatorsAuthenticationProfile{
					UserAssignedIdentities: userAssignedIdentities,
				},
				SubnetID:             &s.SubnetID,
				ManagedResourceGroup: s.getManagedResourceGroup(),
				OutboundType:         outboundType,
				// IssuerURL:            nil,
			},
			//Autoscaling:          nil,
			ClusterImageRegistry: &arohcp.ClusterImageRegistryProfile{
				State: ptr.To(arohcp.ClusterImageRegistryProfileStateEnabled),
			},
			// Capabilities: &arohcp.ClusterCapabilitiesProfile{Disabled: nil},
			DNS: &arohcp.DNSProfile{
				// BaseDomainPrefix: nil,
				// BaseDomain:       nil,
			},
			// azure.etcd_encryption.data_encryption.customer_managed
			Etcd: &arohcp.EtcdProfile{
				DataEncryption: dataEncryption,
			},
			Network: &arohcp.NetworkProfile{
				NetworkType: networkType,
				HostPrefix:  ptr.To(int32(s.Network.HostPrefix)),
				MachineCidr: &s.Network.MachineCIDR,
				PodCidr:     &s.Network.PodCIDR,
				ServiceCidr: &s.Network.ServiceCIDR,
			},
			// NodeDrainTimeoutMinutes: nil,
			Version: &arohcp.VersionProfile{
				ChannelGroup: ptr.To(string(s.ChannelGroup)),
				ID:           &s.Version,
			},
			API: &arohcp.APIProfile{
				Visibility: visibility,
				URL:        nil,
			},
			//Console: &arohcp.ConsoleProfile{URL: nil,},
			//ProvisioningState: nil,
		},
		Tags: s.getTags(),
		// ID:   nil,
		Name: &s.Name,
		// SystemData: &arohcp.SystemData{},
		// Type: nil,
	}
	if existingHcpOpenShiftCluster != nil {
		ret.ID = existingHcpOpenShiftCluster.ID
		changes := []string{}
		immutable := []string{}
		checkChange("location", existingHcpOpenShiftCluster.Location, ret.Location, &changes)
		if existingHcpOpenShiftCluster.Properties == nil {
			changes = append(changes, "adding properties: nil -> new value")
		} else {
			if existingHcpOpenShiftCluster.Properties.Platform == nil {
				changes = append(changes, "adding properties.platform: nil -> new value")
			} else {
				checkImmutable("properties.platform.networkSecurityGroupId", &existingHcpOpenShiftCluster.Properties.Platform.NetworkSecurityGroupID, &ret.Properties.Platform.NetworkSecurityGroupID, &immutable)
				checkChangeIdentities("properties.platform.operatorsAuthentication", existingHcpOpenShiftCluster.Properties.Platform.OperatorsAuthentication, ret.Properties.Platform.OperatorsAuthentication, &changes)
				checkImmutable("properties.platform.subnetID", &existingHcpOpenShiftCluster.Properties.Platform.SubnetID, &ret.Properties.Platform.SubnetID, &immutable)
				checkImmutable("properties.platform.managedResourceGroup", &existingHcpOpenShiftCluster.Properties.Platform.ManagedResourceGroup, &ret.Properties.Platform.ManagedResourceGroup, &immutable)
				checkImmutable("properties.platform.outboundType", &existingHcpOpenShiftCluster.Properties.Platform.OutboundType, &ret.Properties.Platform.OutboundType, &immutable)
			}
			if existingHcpOpenShiftCluster.Properties.Network == nil {
				changes = append(changes, "adding properties.network: nil -> new value")
			} else {
				checkImmutable("properties.network.networkType", &existingHcpOpenShiftCluster.Properties.Network.NetworkType, &ret.Properties.Network.NetworkType, &immutable)
				checkImmutable("properties.network.hostPrefix", &existingHcpOpenShiftCluster.Properties.Network.HostPrefix, &ret.Properties.Network.HostPrefix, &immutable)
				checkImmutable("properties.network.machineCidr", &existingHcpOpenShiftCluster.Properties.Network.MachineCidr, &ret.Properties.Network.MachineCidr, &immutable)
				checkImmutable("properties.network.podCidr", &existingHcpOpenShiftCluster.Properties.Network.PodCidr, &ret.Properties.Network.PodCidr, &immutable)
				checkImmutable("properties.network.serviceCidr", &existingHcpOpenShiftCluster.Properties.Network.ServiceCidr, &ret.Properties.Network.ServiceCidr, &immutable)
			}
			if existingHcpOpenShiftCluster.Properties.Version == nil {
				changes = append(changes, "adding properties.version: nil -> new value")
			} else {
				checkImmutable("properties.version.id", &existingHcpOpenShiftCluster.Properties.Version.ID, &ret.Properties.Version.ID, &immutable)
				checkChange("properties.version.channelGroup", existingHcpOpenShiftCluster.Properties.Version.ChannelGroup, ret.Properties.Version.ChannelGroup, &changes)
			}
			if existingHcpOpenShiftCluster.Properties.API == nil {
				changes = append(changes, "adding properties.API: nil -> new value")
			} else {
				checkImmutable("properties.api.visibility", &existingHcpOpenShiftCluster.Properties.API.Visibility, &ret.Properties.API.Visibility, &immutable)
			}
			checkChangeMap("properties.tags", existingHcpOpenShiftCluster.Tags, ret.Tags, &changes)
		}
		// Log immutable field changes and revert them (no error returned)
		// The checkImmutable() function automatically reverts changes to immutable fields
		// This implements a "log and fix" approach rather than failing the operation
		if len(immutable) > 0 {
			for _, msg := range immutable {
				log.Info(fmt.Sprintf("cannot update immutable field %s", msg))
			}
		}
		if len(changes) == 0 {
			return nil, nil
		}
		for _, msg := range changes {
			log.Info(fmt.Sprintf("changing field %s", msg))
		}
	}
	return ret, nil
}

func ptrToS(a any) string {
	if a == nil {
		return "nil"
	}
	switch msg := a.(type) {
	case *string:
		return fmt.Sprintf("%q", *msg)
	case *int:
		return fmt.Sprintf("%d", *msg)
	default:
		return fmt.Sprintf("%T:???", msg)
	}
}
func checkChange[V comparable](path string, old, updated *V, changes *[]string) bool {
	if ptr.Equal(old, updated) {
		return false
	}
	*changes = append(*changes, fmt.Sprintf("%s: %s -> %s", path, ptrToS(old), ptrToS(updated)))
	return true
}

// checkImmutable detects changes to immutable fields and reverts them.
// This implements a "log and fix" approach:
//  1. Detects if the field value has changed
//  2. Reverts the change by setting updated = old
//  3. Logs the change attempt to the changes slice
//  4. Does NOT return an error - the operation continues
func checkImmutable[V comparable](path string, old, updated **V, changes *[]string) bool {
	if checkChange(path, *old, *updated, changes) {
		*updated = *old
		return true
	}
	return false
}
func checkChangeMap(path string, m1 map[string]*string, m2 map[string]*string, changes *[]string) bool {
	if len(m1) != len(m2) {
		*changes = append(*changes, fmt.Sprintf("%s.len: %d -> %d", path, len(m1), len(m2)))
		return true
	}
	if len(m1) > 0 {
		for k, v := range m1 {
			if !ptr.Equal(m2[k], v) {
				*changes = append(*changes, fmt.Sprintf("%s[%q]: %s -> %s", path, k, ptrToS(v), ptrToS(m2[k])))
				return true
			}
		}
	}
	return false
}

func checkChangeIdentities(path string, a1 *arohcp.OperatorsAuthenticationProfile, a2 *arohcp.OperatorsAuthenticationProfile, changes *[]string) bool {
	j1, _ := a1.MarshalJSON()
	j2, _ := a2.MarshalJSON()
	s1 := string(j1)
	s2 := string(j2)
	if s1 != s2 {
		*changes = append(*changes, fmt.Sprintf("%s: %s -> %s", path, s1, s2))
		return true
	}
	return false
}
