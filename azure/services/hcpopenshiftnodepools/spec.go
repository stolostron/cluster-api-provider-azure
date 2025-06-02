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
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/pkg/errors"
	"k8s.io/utils/ptr"
	expv1 "sigs.k8s.io/cluster-api/exp/api/v1beta1"

	"sigs.k8s.io/cluster-api-provider-azure/exp/api/v1beta2"
	arohcp "sigs.k8s.io/cluster-api-provider-azure/exp/third_party/aro-hcp/api/v20240610preview/generated"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
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

// getAvailabilityZone - converts AvailabilityZone.
func (s *HcpOpenShiftNodePoolSpec) getAvailabilityZone() *string {
	if s.AROMachinePoolSpec.Platform.AvailabilityZone == "" {
		return nil
	}
	return ptr.To(s.AROMachinePoolSpec.Platform.AvailabilityZone)
}

// getAutoScaling converts Autoscaling.
func (s *HcpOpenShiftNodePoolSpec) getAutoScaling() *arohcp.NodePoolAutoScaling {
	if s.AROMachinePoolSpec.Autoscaling == nil {
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
		if effect == nil {
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
func (s *HcpOpenShiftNodePoolSpec) Parameters(ctx context.Context, existing interface{}) (params interface{}, err error) {
	_, log, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftnodepools.Parameters")
	defer done()
	var existingNodePool *arohcp.NodePool
	if existing != nil {
		nodePool, ok := existing.(arohcp.NodePool)
		if !ok {
			return nil, errors.Errorf("%T is not a arohcp.NodePool", existing)
		}
		// NodePool already exists
		existingNodePool = &nodePool
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
	ret := arohcp.NodePool{
		Location: ptr.To(s.Location),
		//Identity: &arohcp.ManagedServiceIdentity{
		//	Type:                   nil,
		//	UserAssignedIdentities: nil,
		//	PrincipalID:            nil,
		//	TenantID:               nil,
		// },
		Properties: &arohcp.NodePoolProperties{
			Platform: &arohcp.NodePoolPlatformProfile{
				VMSize:                 ptr.To(s.AROMachinePoolSpec.Platform.VMSize),
				AvailabilityZone:       s.getAvailabilityZone(),
				EnableEncryptionAtHost: nil,
				OSDisk: &arohcp.OsDiskProfile{
					EncryptionSetID:        nil,
					SizeGiB:                ptr.To(s.AROMachinePoolSpec.Platform.DiskSizeGiB),
					DiskStorageAccountType: diskStorageAccountType,
				},
				SubnetID: ptr.To(s.AROMachinePoolSpec.Platform.Subnet),
			},
			AutoRepair:  to.Ptr(s.AROMachinePoolSpec.AutoRepair),
			AutoScaling: autoScaling,
			Labels:      s.getLabels(),
			Replicas:    replicas,
			Taints:      taints,
			Version: &arohcp.NodePoolVersionProfile{
				ChannelGroup: ptr.To(string(s.AROMachinePoolSpec.ChannelGroup)),
				ID:           ptr.To(s.AROMachinePoolSpec.Version),
			},
			// READ-ONLY: ProvisioningState: nil,
		},
		Tags: s.getTags(),
		// READ-ONLY:
		//   ID:   nil,
		//   Name: nil,
		//   SystemData: &arohcp.SystemData{},
		//   Type: nil,
	}
	if existingNodePool != nil {
		ret.ID = existingNodePool.ID

		// Track changes and immutable field violations
		changes := []string{}
		immutable := []string{}

		// TODO: why is existingNodePool.Location == nil :(
		// checkChange("location", existingNodePool.Location, ret.Location, &changes)

		if existingNodePool.Properties == nil {
			changes = append(changes, "adding properties: nil -> new value")
		} else {
			if existingNodePool.Properties.Platform == nil {
				changes = append(changes, "adding properties.platform: nil -> new value")
			} else {
				// Platform fields - most are immutable per TypeSpec
				checkImmutable("properties.platform.vmSize", &existingNodePool.Properties.Platform.VMSize, &ret.Properties.Platform.VMSize, &immutable)
				checkImmutable("properties.platform.availabilityZone", &existingNodePool.Properties.Platform.AvailabilityZone, &ret.Properties.Platform.AvailabilityZone, &immutable)
				checkImmutable("properties.platform.subnetID", &existingNodePool.Properties.Platform.SubnetID, &ret.Properties.Platform.SubnetID, &immutable)

				if existingNodePool.Properties.Platform.OSDisk == nil {
					changes = append(changes, "adding properties.platform.osDisk: nil -> new value")
				} else {
					checkImmutable("properties.platform.osDisk.sizeGiB", &existingNodePool.Properties.Platform.OSDisk.SizeGiB, &ret.Properties.Platform.OSDisk.SizeGiB, &immutable)
					checkImmutable("properties.platform.osDisk.diskStorageAccountType", &existingNodePool.Properties.Platform.OSDisk.DiskStorageAccountType, &ret.Properties.Platform.OSDisk.DiskStorageAccountType, &immutable)
				}
			}

			// AutoRepair is immutable per TypeSpec
			checkImmutable("properties.autoRepair", &existingNodePool.Properties.AutoRepair, &ret.Properties.AutoRepair, &immutable)

			// Version handling - id is immutable, channelGroup is mutable
			// NodePool uses NodePoolVersionProfile where both fields have @visibility(Lifecycle.Update)
			if existingNodePool.Properties.Version == nil {
				changes = append(changes, "adding properties.version: nil -> new value")
			} else {
				checkImmutable("properties.version.id", &existingNodePool.Properties.Version.ID, &ret.Properties.Version.ID, &immutable)
				checkChange("properties.version.channelGroup", existingNodePool.Properties.Version.ChannelGroup, ret.Properties.Version.ChannelGroup, &changes)
			}

			// Mutable fields - these can be changed
			checkChangeAutoScaling("properties.autoScaling", existingNodePool.Properties.AutoScaling, ret.Properties.AutoScaling, &changes)
			checkChangeLabels("properties.labels", existingNodePool.Properties.Labels, ret.Properties.Labels, &changes)
			checkChange("properties.replicas", existingNodePool.Properties.Replicas, ret.Properties.Replicas, &changes)
			checkChangeTaints("properties.taints", existingNodePool.Properties.Taints, ret.Properties.Taints, &changes)
		}

		checkChangeMap("tags", existingNodePool.Tags, ret.Tags, &changes)

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

// Helper functions for change detection and immutability enforcement.

const nilString = "nil"

func ptrToS(a any) string {
	if a == nil {
		return nilString
	}
	switch msg := a.(type) {
	case *string:
		if msg == nil {
			return nilString
		}
		return fmt.Sprintf("%q", *msg)
	case *int32:
		if msg == nil {
			return nilString
		}
		return fmt.Sprintf("%d", *msg)
	case *bool:
		if msg == nil {
			return nilString
		}
		return fmt.Sprintf("%t", *msg)
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

func checkChangeAutoScaling(path string, a1, a2 *arohcp.NodePoolAutoScaling, changes *[]string) bool {
	if (a1 == nil) != (a2 == nil) {
		*changes = append(*changes, fmt.Sprintf("%s: %s -> %s", path, ptrToS(a1), ptrToS(a2)))
		return true
	}
	if a1 != nil && a2 != nil {
		if checkChange(fmt.Sprintf("%s.min", path), a1.Min, a2.Min, changes) ||
			checkChange(fmt.Sprintf("%s.max", path), a1.Max, a2.Max, changes) {
			return true
		}
	}
	return false
}

func checkChangeLabels(path string, l1, l2 []*arohcp.Label, changes *[]string) bool {
	if len(l1) != len(l2) {
		*changes = append(*changes, fmt.Sprintf("%s.len: %d -> %d", path, len(l1), len(l2)))
		return true
	}
	if len(l1) > 0 {
		for _, label1 := range l1 {
			found := false
			for _, label2 := range l2 {
				if ptr.Equal(label1.Key, label2.Key) && ptr.Equal(label1.Value, label2.Value) {
					found = true
					break
				}
			}
			if !found {
				*changes = append(*changes, fmt.Sprintf("%s: label changed", path))
				return true
			}
		}
	}
	return false
}

func checkChangeTaints(path string, t1, t2 []*arohcp.Taint, changes *[]string) bool {
	if len(t1) != len(t2) {
		*changes = append(*changes, fmt.Sprintf("%s.len: %d -> %d", path, len(t1), len(t2)))
		return true
	}
	if len(t1) > 0 {
		for _, taint1 := range t1 {
			found := false
			for _, taint2 := range t2 {
				if ptr.Equal(taint1.Key, taint2.Key) &&
					ptr.Equal(taint1.Value, taint2.Value) &&
					ptr.Equal(taint1.Effect, taint2.Effect) {
					found = true
					break
				}
			}
			if !found {
				*changes = append(*changes, fmt.Sprintf("%s: taint changed", path))
				return true
			}
		}
	}
	return false
}
