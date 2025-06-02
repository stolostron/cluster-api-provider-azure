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
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
	"k8s.io/utils/ptr"

	cplane "sigs.k8s.io/cluster-api-provider-azure/exp/api/controlplane/v1beta2"
	"sigs.k8s.io/cluster-api-provider-azure/exp/api/v1beta2"
)

func TestParameters(t *testing.T) {
	testcases := []struct {
		name     string
		spec     HcpOpenShiftClustersSpec
		existing interface{}
		expected interface{}
		errorMsg string
	}{
		{
			name:     "no existing HcpOpenShiftCluster",
			spec:     fakeHcpOpenShiftClustersSpec(),
			existing: nil,
			expected: fakeHcpOpenShiftCluster(),
		},
		{
			name:     "existing is not a HcpOpenShiftCluster",
			spec:     fakeHcpOpenShiftClustersSpec(),
			existing: "wrong type",
			errorMsg: "string is not a arohcp.HcpOpenShiftCluster",
		},
		{
			name:     "existing HcpOpenShiftCluster - no changes",
			spec:     fakeHcpOpenShiftClustersSpec(),
			existing: fakeExistingHcpOpenShiftCluster(),
			expected: nil,
		},
		{
			name:     "unsupported outbound type",
			spec:     fakeHcpOpenShiftClustersSpecWithInvalidOutboundType(),
			existing: nil,
			errorMsg: "unsupported outbound type InvalidType",
		},
		{
			name:     "unsupported network type",
			spec:     fakeHcpOpenShiftClustersSpecWithInvalidNetworkType(),
			existing: nil,
			errorMsg: "unsupported network type InvalidType",
		},
		{
			name:     "unsupported visibility type",
			spec:     fakeHcpOpenShiftClustersSpecWithInvalidVisibility(),
			existing: nil,
			errorMsg: "unsupported visibility type InvalidType",
		},
		{
			name:     "immutable managedResourceGroup changed - should not error",
			spec:     fakeHcpOpenShiftClustersSpecWithDifferentManagedRG(),
			existing: fakeExistingHcpOpenShiftCluster(),
			expected: fakeHcpOpenShiftClusterWithDifferentManagedRG(),
		},
		{
			name:     "immutable networkSecurityGroupId changed - should not error",
			spec:     fakeHcpOpenShiftClustersSpecWithDifferentNSG(),
			existing: fakeExistingHcpOpenShiftCluster(),
			expected: fakeHcpOpenShiftClusterWithDifferentNSG(),
		},
	}
	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			t.Parallel()

			result, err := tc.spec.Parameters(t.Context(), tc.existing)
			if tc.errorMsg != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.errorMsg))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
			if !reflect.DeepEqual(result, tc.expected) {
				t.Errorf("Got difference between expected result and computed result:\n%s", cmp.Diff(tc.expected, result))
			}
		})
	}
}

func fakeHcpOpenShiftClustersSpec() HcpOpenShiftClustersSpec {
	return HcpOpenShiftClustersSpec{
		Name:              "test-cluster",
		Location:          "eastus",
		ResourceGroup:     "test-rg",
		NodeResourceGroup: "test-node-rg",
		ManagedIdentities: &cplane.ManagedIdentities{
			ControlPlaneOperators: &cplane.ControlPlaneOperators{
				ControlPlaneManagedIdentities:           "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/control-plane",
				ClusterAPIAzureManagedIdentities:        "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cluster-api-azure",
				CloudControllerManagerManagedIdentities: "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cloud-controller-manager",
				IngressManagedIdentities:                "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/ingress",
				DiskCsiDriverManagedIdentities:          "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/disk-csi-driver",
				FileCsiDriverManagedIdentities:          "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/file-csi-driver",
				ImageRegistryManagedIdentities:          "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/image-registry",
				CloudNetworkConfigManagedIdentities:     "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/cloud-network-config",
				KmsManagedIdentities:                    "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/kms",
			},
			DataPlaneOperators: &cplane.DataPlaneOperators{
				DiskCsiDriverManagedIdentities: "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/data-disk-csi-driver",
				FileCsiDriverManagedIdentities: "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/data-file-csi-driver",
				ImageRegistryManagedIdentities: "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/data-image-registry",
			},
			ServiceManagedIdentity: "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.ManagedIdentity/userAssignedIdentities/service",
		},
		AdditionalTags: map[string]string{
			"test-key": "test-value",
		},
		SubscriptionID:         "test",
		NetworkSecurityGroupID: "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/test-nsg",
		SubnetID:               "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
		VNetID:                 "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet",
		VaultID:                "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.KeyVault/vaults/test-kv",
		VaultName:              ptr.To("test-kv"),
		VaultKeyName:           ptr.To("etcd-data-kms-encryption-key"),
		VaultKeyVersion:        ptr.To("test-key-version"),
		OutboundType:           "LoadBalancer",
		Network: &cplane.NetworkSpec{
			NetworkType: "OVNKubernetes",
			MachineCIDR: "10.0.0.0/16",
			ServiceCIDR: "172.30.0.0/16",
			PodCIDR:     "10.128.0.0/14",
			HostPrefix:  23,
		},
		Version:      "4.19",
		ChannelGroup: v1beta2.Stable,
		Visibility:   "Public",
	}
}

func fakeHcpOpenShiftClustersSpecWithInvalidOutboundType() HcpOpenShiftClustersSpec {
	spec := fakeHcpOpenShiftClustersSpec()
	spec.OutboundType = "InvalidType"
	return spec
}

func fakeHcpOpenShiftClustersSpecWithInvalidNetworkType() HcpOpenShiftClustersSpec {
	spec := fakeHcpOpenShiftClustersSpec()
	spec.Network.NetworkType = "InvalidType"
	return spec
}

func fakeHcpOpenShiftClustersSpecWithInvalidVisibility() HcpOpenShiftClustersSpec {
	spec := fakeHcpOpenShiftClustersSpec()
	spec.Visibility = "InvalidType"
	return spec
}

func fakeHcpOpenShiftClustersSpecWithDifferentManagedRG() HcpOpenShiftClustersSpec {
	spec := fakeHcpOpenShiftClustersSpec()
	spec.NodeResourceGroup = "different-node-rg"
	return spec
}

func fakeHcpOpenShiftClustersSpecWithDifferentNSG() HcpOpenShiftClustersSpec {
	spec := fakeHcpOpenShiftClustersSpec()
	spec.NetworkSecurityGroupID = "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.Network/networkSecurityGroups/different-nsg"
	return spec
}

func fakeHcpOpenShiftClusterWithDifferentManagedRG() interface{} {
	// This should return nil since immutable fields are reverted, no changes should be made
	return nil
}

func fakeHcpOpenShiftClusterWithDifferentNSG() interface{} {
	// This should return nil since immutable fields are reverted, no changes should be made
	return nil
}
