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
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/cluster-api-provider-azure/exp/api/v1beta2"
	arohcp "sigs.k8s.io/cluster-api-provider-azure/exp/third_party/aro-hcp/api/v20240610preview/armredhatopenshifthcp"
)

func TestParameters(t *testing.T) {
	testcases := []struct {
		name     string
		spec     HcpOpenShiftNodePoolSpec
		existing interface{}
		expected interface{}
		errorMsg string
	}{
		{
			name:     "no existing NodePool",
			spec:     *fakeHcpOpenShiftNodePoolSpec(),
			existing: nil,
			expected: fakeHcpOpenShiftNodePool(),
		},
		{
			name:     "existing is not a NodePool",
			spec:     *fakeHcpOpenShiftNodePoolSpec(),
			existing: "wrong type",
			errorMsg: "string is not a arohcp.NodePool",
		},
		{
			name:     "existing NodePool - no changes",
			spec:     *fakeHcpOpenShiftNodePoolSpec(),
			existing: fakeExistingHcpOpenShiftNodePool(),
			expected: nil,
		},
		{
			name:     "unsupported disk storage account type",
			spec:     fakeHcpOpenShiftNodePoolSpecWithInvalidDiskType(),
			existing: nil,
			errorMsg: "unsupported DiskStorageAccountType InvalidType",
		},
		{
			name:     "invalid taint effect",
			spec:     fakeHcpOpenShiftNodePoolSpecWithInvalidTaint(),
			existing: nil,
			errorMsg: "no taint found for effect InvalidEffect",
		},
		{
			name:     "with autoscaling",
			spec:     fakeHcpOpenShiftNodePoolSpecWithAutoscaling(),
			existing: nil,
			expected: fakeHcpOpenShiftNodePoolWithAutoscaling(),
		},
		{
			name:     "with taints",
			spec:     fakeHcpOpenShiftNodePoolSpecWithTaints(),
			existing: nil,
			expected: fakeHcpOpenShiftNodePoolWithTaints(),
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

func fakeHcpOpenShiftNodePoolSpecWithInvalidDiskType() HcpOpenShiftNodePoolSpec {
	spec := *fakeHcpOpenShiftNodePoolSpec()
	spec.AROMachinePoolSpec.Platform.DiskStorageAccountType = "InvalidType"
	return spec
}

func fakeHcpOpenShiftNodePoolSpecWithInvalidTaint() HcpOpenShiftNodePoolSpec {
	spec := *fakeHcpOpenShiftNodePoolSpec()
	spec.AROMachinePoolSpec.Taints = []v1beta2.AROTaint{
		{
			Key:    "test-key",
			Value:  "test-value",
			Effect: "InvalidEffect",
		},
	}
	return spec
}

func fakeHcpOpenShiftNodePoolSpecWithAutoscaling() HcpOpenShiftNodePoolSpec {
	spec := *fakeHcpOpenShiftNodePoolSpec()
	spec.AROMachinePoolSpec.Autoscaling = &v1beta2.AROMachinePoolAutoScaling{
		MinReplicas: 1,
		MaxReplicas: 10,
	}
	// When autoscaling is enabled, replicas should be nil
	spec.MachinePoolSpec.Replicas = nil
	return spec
}

func fakeHcpOpenShiftNodePoolWithAutoscaling() arohcp.NodePool {
	nodePool := fakeHcpOpenShiftNodePool()
	nodePool.Properties.AutoScaling = &arohcp.NodePoolAutoScaling{
		Min: ptr.To[int32](1),
		Max: ptr.To[int32](10),
	}
	nodePool.Properties.Replicas = nil
	return nodePool
}

func fakeHcpOpenShiftNodePoolSpecWithTaints() HcpOpenShiftNodePoolSpec {
	spec := *fakeHcpOpenShiftNodePoolSpec()
	spec.AROMachinePoolSpec.Taints = []v1beta2.AROTaint{
		{
			Key:    "test-key",
			Value:  "test-value",
			Effect: corev1.TaintEffectNoSchedule,
		},
	}
	return spec
}

func fakeHcpOpenShiftNodePoolWithTaints() arohcp.NodePool {
	nodePool := fakeHcpOpenShiftNodePool()
	effect := arohcp.EffectNoSchedule
	nodePool.Properties.Taints = []*arohcp.Taint{
		{
			Key:    ptr.To("test-key"),
			Value:  ptr.To("test-value"),
			Effect: &effect,
		},
	}
	return nodePool
}
