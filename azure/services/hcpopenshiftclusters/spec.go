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
	arohcp "github.com/marek-veber/ARO-HCP/external/api/v20240610preview/generated"
	"github.com/pkg/errors"
	"k8s.io/utils/ptr"
	cplane "sigs.k8s.io/cluster-api-provider-azure/exp/api/controlplane/v1beta2"
)

// HcpOpenShiftClustersSpec defines the specification for a HcpOpenShiftCluster.
type HcpOpenShiftClustersSpec struct {
	Name                   string
	Location               string
	ResourceGroup          string
	ManagedIdentities      *cplane.ManagedIdentities
	AdditionalTags         map[string]string
	NetworkSecurityGroupID string
	Subnet                 string
	OutboundType           string
	Network                *cplane.NetworkSpec
	Version                string
	ChannelGroup           cplane.ChannelGroupType
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

// getManagedIdentities converts managed identities
func (s *HcpOpenShiftClustersSpec) getManagedIdentities() (*arohcp.UserAssignedIdentitiesProfile, *arohcp.ManagedServiceIdentity) {
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
			// TODO: mveber - update mohamed's proposal - kms should be removed
			// "kms":                      &s.ManagedIdentities.ControlPlaneOperators.KmsManagedIdentities,
		},
		DataPlaneOperators: map[string]*string{
			"disk-csi-driver": &s.ManagedIdentities.DataPlaneOperators.DiskCsiDriverManagedIdentities,
			"file-csi-driver": &s.ManagedIdentities.DataPlaneOperators.FileCsiDriverManagedIdentities,
			"image-registry":  &s.ManagedIdentities.DataPlaneOperators.ImageRegistryManagedIdentities,
		},
		ServiceManagedIdentity: &s.ManagedIdentities.ServiceManagedIdentity,
	}
	managedServiceIdentity := &arohcp.ManagedServiceIdentity{
		Type:                   &managedServiceIdentityType,
		UserAssignedIdentities: map[string]*arohcp.UserAssignedIdentity{},
		// PrincipalID:            nil,
		// TenantID:               nil,
	}
	midsMap := []map[string]*string{
		userAssignedIdentities.ControlPlaneOperators,
		// TODO: mveber - why it cannot be here		userAssignedIdentities.DataPlaneOperators,
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

// getTags - convert AdditionalTags
func (s *HcpOpenShiftClustersSpec) getTags() map[string]*string {
	ret := map[string]*string{}
	for k, v := range s.AdditionalTags {
		ret[k] = &v
	}
	return ret
}

// getManagedResourceGroup - returns manager resource group name
func (s *HcpOpenShiftClustersSpec) getManagedResourceGroup() *string {
	managedResourceGroup := fmt.Sprintf("__capz_aro_managed_%s_rg", s.Name)
	return &managedResourceGroup
}

// getManagedResourceGroup - returns manager resource group name
func (s *HcpOpenShiftClustersSpec) getOutboundType() (*arohcp.OutboundType, error) {
	if s.OutboundType == "loadBalancer" {
		outboundType := arohcp.OutboundTypeLoadBalancer
		return &outboundType, nil
	}
	return nil, errors.Errorf("unsupported outbound type %s", s.OutboundType)
}

// getNetworkType - returns network type
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

// getVisibility - returns visibility type
func (s *HcpOpenShiftClustersSpec) getVisibility() (*arohcp.Visibility, error) {
	if s.Visibility == "private" {
		visibility := arohcp.VisibilityPrivate
		return &visibility, nil
	}
	if s.Visibility == "public" {
		visibility := arohcp.VisibilityPublic
		return &visibility, nil
	}
	return nil, errors.Errorf("unsupported visibilit type %s", s.Visibility)
}

// Parameters returns the parameters for the HcpOpenShiftCluster.
func (s *HcpOpenShiftClustersSpec) Parameters(_ context.Context, existing interface{}) (params interface{}, err error) {
	if existing != nil {
		existingHcpOpenShiftCluster, ok := existing.(arohcp.HcpOpenShiftCluster)
		if !ok {
			return nil, errors.Errorf("%T is not a arohcp.HcpOpenShiftCluster", existing)
		}
		// HcpOpenShiftCluster group already exists
		_ = existingHcpOpenShiftCluster
		return nil, nil // TODO mveber - update
	}

	userAssignedIdentities, managedServiceIdentity := s.getManagedIdentities()
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

	return arohcp.HcpOpenShiftCluster{
		Location: ptr.To(s.Location),
		Identity: managedServiceIdentity,
		Properties: &arohcp.HcpOpenShiftClusterProperties{
			Platform: &arohcp.PlatformProfile{
				NetworkSecurityGroupID: &s.NetworkSecurityGroupID,
				OperatorsAuthentication: &arohcp.OperatorsAuthenticationProfile{
					UserAssignedIdentities: userAssignedIdentities,
				},
				SubnetID:             &s.Subnet,
				ManagedResourceGroup: s.getManagedResourceGroup(),
				OutboundType:         outboundType,
				// IssuerURL:            nil,
			},
			// Capabilities: &arohcp.ClusterCapabilitiesProfile{Disabled: nil},
			DNS: &arohcp.DNSProfile{
				// BaseDomainPrefix: nil,
				// BaseDomain:       nil,
			},
			Network: &arohcp.NetworkProfile{
				HostPrefix:  ptr.To(int32(s.Network.HostPrefix)),
				MachineCidr: &s.Network.MachineCIDR,
				NetworkType: networkType,
				PodCidr:     &s.Network.PodCIDR,
				ServiceCidr: &s.Network.ServiceCIDR,
			},
			Version: &arohcp.VersionProfile{
				ChannelGroup:      ptr.To(string(s.ChannelGroup)),
				ID:                &s.Version,
				AvailableUpgrades: nil,
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
	}, nil
}
