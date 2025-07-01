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

package hcpopenshiftnodepools

import (
	"context"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	arohcp "github.com/marek-veber/ARO-HCP/external/api/v20240610preview/generated"
	"github.com/pkg/errors"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/cluster-api-provider-azure/exp/api/v1beta2"
	expv1 "sigs.k8s.io/cluster-api/exp/api/v1beta1"
)

// HcpOpenShiftNodePoolSpec defines the specification for a NodePool.
type HcpOpenShiftNodePoolSpec struct {
	ClusterName        string
	Location           string
	ResourceGroup      string
	AROMachinePoolSpec v1beta2.AROMachinePoolSpec
	MachinePoolSpec    expv1.MachinePoolSpec
}

// ResourceName returns the name of the NodePool.
func (s *HcpOpenShiftNodePoolSpec) ResourceName() string {
	return s.AROMachinePoolSpec.NodePoolName
}

// ResourceGroupName returns the name of the resource group.
func (s *HcpOpenShiftNodePoolSpec) ResourceGroupName() string {
	return s.ResourceGroup
}

// OwnerResourceName returns the cluster name.
func (s *HcpOpenShiftNodePoolSpec) OwnerResourceName() string {
	return s.ClusterName
}

// getTags converts AdditionalTags.
func (s *HcpOpenShiftNodePoolSpec) getTags() map[string]*string {
	ret := map[string]*string{}
	for k, v := range s.AROMachinePoolSpec.AdditionalTags {
		ret[k] = &v
	}
	return ret
}

// getDiskStorageAccountType converts DiskStorageAccountType.
func (s *HcpOpenShiftNodePoolSpec) getDiskStorageAccountType() (*arohcp.DiskStorageAccountType, error) {
	for _, at := range arohcp.PossibleDiskStorageAccountTypeValues() {
		if string(at) == s.AROMachinePoolSpec.Platform.DiskStorageAccountType {
			return &at, nil
		}
	}
	return nil, errors.Errorf("unsupported DiskStorageAccountType %s", s.AROMachinePoolSpec.Platform.DiskStorageAccountType)
}

// getAvailabilityZone - converts AvailabilityZone
func (s *HcpOpenShiftNodePoolSpec) getAvailabilityZone() *string {
	if s.AROMachinePoolSpec.Platform.AvailabilityZone == "" {
		return nil
	}
	return ptr.To(s.AROMachinePoolSpec.Platform.AvailabilityZone)
}

// getAutoScaling converts Autoscaling.
func (s *HcpOpenShiftNodePoolSpec) getAutoScaling() *arohcp.NodePoolAutoScaling {
	if nil == s.AROMachinePoolSpec.Autoscaling {
		return nil
	}
	return &arohcp.NodePoolAutoScaling{
		Max: to.Ptr(int32(s.AROMachinePoolSpec.Autoscaling.MaxReplicas)),
		Min: to.Ptr(int32(s.AROMachinePoolSpec.Autoscaling.MinReplicas)),
	}
}

// getTaints converts Taints.
func (s *HcpOpenShiftNodePoolSpec) getTaints() ([]*arohcp.Taint, error) {
	var ret []*arohcp.Taint
	for _, t := range s.AROMachinePoolSpec.Taints {
		var effect *arohcp.Effect
		for _, e := range arohcp.PossibleEffectValues() {
			if string(e) == string(t.Effect) {
				effect = &e
			}
		}
		if nil == effect {
			return nil, errors.Errorf("no taint found for effect %s", t.Effect)
		}
		ret = append(ret, &arohcp.Taint{Effect: effect, Key: to.Ptr(t.Key), Value: to.Ptr(t.Value)})
	}
	return ret, nil
}

// getLabels converts Labels.
func (s *HcpOpenShiftNodePoolSpec) getLabels() []*arohcp.Label {
	var ret []*arohcp.Label
	for k, v := range s.AROMachinePoolSpec.Labels {
		ret = append(ret, &arohcp.Label{Key: to.Ptr(k), Value: to.Ptr(v)})
	}
	return ret
}

// Parameters returns the parameters for the NodePool.
func (s *HcpOpenShiftNodePoolSpec) Parameters(_ context.Context, existing interface{}) (params interface{}, err error) {
	if existing != nil {
		existingNodePool, ok := existing.(arohcp.NodePool)
		if !ok {
			return nil, errors.Errorf("%T is not a arohcp.NodePool", existing)
		}
		// NodePool already exists
		_ = existingNodePool
		return nil, nil // TODO mveber - update
	}

	diskStorageAccountType, err := s.getDiskStorageAccountType()
	if err != nil {
		return nil, err
	}

	taints, err := s.getTaints()
	if err != nil {
		return nil, err
	}

	replicas := s.MachinePoolSpec.Replicas
	autoScaling := s.getAutoScaling()
	if autoScaling != nil {
		replicas = nil
	}

	return arohcp.NodePool{
		Location: ptr.To(s.Location),
		//Identity: &arohcp.ManagedServiceIdentity{
		//	Type:                   nil,
		//	UserAssignedIdentities: nil,
		//	PrincipalID:            nil,
		//	TenantID:               nil,
		//},
		Properties: &arohcp.NodePoolProperties{
			Platform: &arohcp.NodePoolPlatformProfile{
				VMSize:                 ptr.To(s.AROMachinePoolSpec.Platform.VMSize),
				AvailabilityZone:       s.getAvailabilityZone(),
				DiskSizeGiB:            ptr.To(s.AROMachinePoolSpec.Platform.DiskSizeGiB),
				DiskStorageAccountType: diskStorageAccountType,
				SubnetID:               ptr.To(s.AROMachinePoolSpec.Platform.Subnet),
			},
			AutoRepair:  to.Ptr(s.AROMachinePoolSpec.AutoRepair),
			AutoScaling: autoScaling,
			Labels:      s.getLabels(),
			Replicas:    replicas,
			Taints:      taints,
			Version: &arohcp.NodePoolVersionProfile{
				ChannelGroup:      ptr.To(string(s.AROMachinePoolSpec.ChannelGroup)),
				ID:                ptr.To(s.AROMachinePoolSpec.Version),
				AvailableUpgrades: nil,
			},
			// READ-ONLY: ProvisioningState: nil,
		},
		Tags: s.getTags(),
		// READ-ONLY:
		//   ID:   nil,
		//   Name: nil,
		//   SystemData: &arohcp.SystemData{},
		//   Type: nil,
	}, nil
}
