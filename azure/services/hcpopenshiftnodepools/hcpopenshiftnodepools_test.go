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
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"k8s.io/utils/ptr"
	expv1 "sigs.k8s.io/cluster-api/exp/api/v1beta1"

	infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/async/mock_async"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/hcpopenshiftnodepools/mock_hcpopenshiftnodepools"
	cplane "sigs.k8s.io/cluster-api-provider-azure/exp/api/controlplane/v1beta2"
	"sigs.k8s.io/cluster-api-provider-azure/exp/api/v1beta2"
	arohcp "sigs.k8s.io/cluster-api-provider-azure/exp/third_party/aro-hcp/api/v20240610preview/armredhatopenshifthcp"
	gomockinternal "sigs.k8s.io/cluster-api-provider-azure/internal/test/matchers/gomock"
	"sigs.k8s.io/cluster-api-provider-azure/util/reconciler"
)

var (
	fakeGroupName    = "my-rg"
	fakeClusterName  = "test-cluster"
	fakeNodePoolName = "test-nodepool"

	notFoundError = &azcore.ResponseError{StatusCode: http.StatusNotFound}
)

func TestReconcileHcpOpenShiftNodePool(t *testing.T) {
	testcases := []struct {
		name          string
		expectedError string
		expect        func(s *mock_hcpopenshiftnodepools.MockHcpOpenShiftNodePoolScopeMockRecorder,
			m *mock_hcpopenshiftnodepools.MockClientMockRecorder,
			r *mock_async.MockReconcilerMockRecorder)
	}{
		{
			name:          "NodePool successfully created",
			expectedError: "",
			expect: func(s *mock_hcpopenshiftnodepools.MockHcpOpenShiftNodePoolScopeMockRecorder,
				m *mock_hcpopenshiftnodepools.MockClientMockRecorder,
				r *mock_async.MockReconcilerMockRecorder) {
				s.DefaultedAzureServiceReconcileTimeout().Return(reconciler.DefaultAzureServiceReconcileTimeout)
				fakeSpec := fakeHcpOpenShiftNodePoolSpec()
				s.NodePoolSpecs(gomockinternal.AContext()).AnyTimes().Return(fakeSpec)
				m.Get(gomockinternal.AContext(), fakeSpec).Return(nil, notFoundError)
				r.CreateOrUpdateResource(gomockinternal.AContext(), fakeSpec, serviceName).Return(fakeHcpOpenShiftNodePool(), nil)
				s.UpdatePutStatus(infrav1.BootstrapSucceededCondition, serviceName, nil)
				s.SetProvisioningState(gomock.Any()).AnyTimes()
				s.SetStatusVersion(gomock.Any()).AnyTimes()
			},
		},
		{
			name:          "NodePool already exists - get existing",
			expectedError: "",
			expect: func(s *mock_hcpopenshiftnodepools.MockHcpOpenShiftNodePoolScopeMockRecorder,
				m *mock_hcpopenshiftnodepools.MockClientMockRecorder,
				r *mock_async.MockReconcilerMockRecorder) {
				s.DefaultedAzureServiceReconcileTimeout().Return(reconciler.DefaultAzureServiceReconcileTimeout)
				fakeSpec := fakeHcpOpenShiftNodePoolSpec()
				s.NodePoolSpecs(gomockinternal.AContext()).AnyTimes().Return(fakeSpec)
				existingNodePool := fakeExistingHcpOpenShiftNodePool()
				m.Get(gomockinternal.AContext(), fakeSpec).Return(existingNodePool, nil)
				s.SetProvisioningState(existingNodePool.Properties.ProvisioningState).AnyTimes()
				s.SetStatusVersion(existingNodePool.Properties.Version).AnyTimes()
				r.CreateOrUpdateResource(gomockinternal.AContext(), fakeSpec, serviceName).Return(existingNodePool, nil)
				s.UpdatePutStatus(infrav1.BootstrapSucceededCondition, serviceName, nil)
			},
		},
		{
			name:          "fail to get existing nodepool",
			expectedError: `failed to get existing NodePool:.*#: Internal Server Error: StatusCode=500`,
			expect: func(s *mock_hcpopenshiftnodepools.MockHcpOpenShiftNodePoolScopeMockRecorder,
				m *mock_hcpopenshiftnodepools.MockClientMockRecorder,
				r *mock_async.MockReconcilerMockRecorder) {
				s.DefaultedAzureServiceReconcileTimeout().Return(reconciler.DefaultAzureServiceReconcileTimeout)
				fakeSpec := fakeHcpOpenShiftNodePoolSpec()
				s.NodePoolSpecs(gomockinternal.AContext()).AnyTimes().Return(fakeSpec)
				m.Get(gomockinternal.AContext(), fakeSpec).Return(nil, &azcore.ResponseError{
					RawResponse: &http.Response{
						Body:       io.NopCloser(strings.NewReader("#: Internal Server Error: StatusCode=500")),
						StatusCode: http.StatusInternalServerError,
					},
				})
				// No UpdatePutStatus call expected because function returns early on Get error
			},
		},
		{
			name:          "fail to create nodepool",
			expectedError: "#: Internal Server Error: StatusCode=500",
			expect: func(s *mock_hcpopenshiftnodepools.MockHcpOpenShiftNodePoolScopeMockRecorder,
				m *mock_hcpopenshiftnodepools.MockClientMockRecorder,
				r *mock_async.MockReconcilerMockRecorder) {
				s.DefaultedAzureServiceReconcileTimeout().Return(reconciler.DefaultAzureServiceReconcileTimeout)
				fakeSpec := fakeHcpOpenShiftNodePoolSpec()
				s.NodePoolSpecs(gomockinternal.AContext()).AnyTimes().Return(fakeSpec)
				m.Get(gomockinternal.AContext(), fakeSpec).Return(nil, notFoundError)
				createError := &azcore.ResponseError{
					RawResponse: &http.Response{
						Body:       io.NopCloser(strings.NewReader("#: Internal Server Error: StatusCode=500")),
						StatusCode: http.StatusInternalServerError,
					},
				}
				r.CreateOrUpdateResource(gomockinternal.AContext(), fakeSpec, serviceName).Return(nil, createError)
				s.UpdatePutStatus(infrav1.BootstrapSucceededCondition, serviceName, createError)
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			t.Parallel()
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()
			scopeMock := mock_hcpopenshiftnodepools.NewMockHcpOpenShiftNodePoolScope(mockCtrl)
			clientMock := mock_hcpopenshiftnodepools.NewMockClient(mockCtrl)
			asyncMock := mock_async.NewMockReconciler(mockCtrl)

			tc.expect(scopeMock.EXPECT(), clientMock.EXPECT(), asyncMock.EXPECT())

			s := &Service{
				Scope:      scopeMock,
				Client:     clientMock,
				Reconciler: asyncMock,
			}

			err := s.Reconcile(t.Context())
			if tc.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(strings.ReplaceAll(err.Error(), "\n", "")).To(MatchRegexp(tc.expectedError))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestDeleteHcpOpenShiftNodePool(t *testing.T) {
	testcases := []struct {
		name          string
		expectedError string
		expect        func(s *mock_hcpopenshiftnodepools.MockHcpOpenShiftNodePoolScopeMockRecorder,
			m *mock_hcpopenshiftnodepools.MockClientMockRecorder,
			r *mock_async.MockReconcilerMockRecorder)
	}{
		{
			name:          "successfully delete an existing nodepool",
			expectedError: "",
			expect: func(s *mock_hcpopenshiftnodepools.MockHcpOpenShiftNodePoolScopeMockRecorder,
				m *mock_hcpopenshiftnodepools.MockClientMockRecorder,
				r *mock_async.MockReconcilerMockRecorder) {
				s.DefaultedAzureServiceReconcileTimeout().Return(reconciler.DefaultAzureServiceReconcileTimeout)
				fakeSpec := fakeHcpOpenShiftNodePoolSpec()
				s.NodePoolSpecs(gomockinternal.AContext()).AnyTimes().Return(fakeSpec)
				gomock.InOrder(
					r.DeleteResource(gomockinternal.AContext(), fakeSpec, serviceName).Return(nil),
					s.UpdateDeleteStatus(infrav1.BootstrapSucceededCondition, serviceName, nil),
				)
			},
		},
		{
			name:          "nodepool deletion fails",
			expectedError: "#: Internal Server Error: StatusCode=500",
			expect: func(s *mock_hcpopenshiftnodepools.MockHcpOpenShiftNodePoolScopeMockRecorder,
				m *mock_hcpopenshiftnodepools.MockClientMockRecorder,
				r *mock_async.MockReconcilerMockRecorder) {
				s.DefaultedAzureServiceReconcileTimeout().Return(reconciler.DefaultAzureServiceReconcileTimeout)
				fakeSpec := fakeHcpOpenShiftNodePoolSpec()
				s.NodePoolSpecs(gomockinternal.AContext()).AnyTimes().Return(fakeSpec)
				deleteError := &azcore.ResponseError{
					RawResponse: &http.Response{
						Body:       io.NopCloser(strings.NewReader("#: Internal Server Error: StatusCode=500")),
						StatusCode: http.StatusInternalServerError,
					},
				}
				gomock.InOrder(
					r.DeleteResource(gomockinternal.AContext(), fakeSpec, serviceName).Return(deleteError),
					s.UpdateDeleteStatus(infrav1.BootstrapSucceededCondition, serviceName, deleteError),
				)
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			t.Parallel()
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()
			scopeMock := mock_hcpopenshiftnodepools.NewMockHcpOpenShiftNodePoolScope(mockCtrl)
			clientMock := mock_hcpopenshiftnodepools.NewMockClient(mockCtrl)
			asyncMock := mock_async.NewMockReconciler(mockCtrl)

			tc.expect(scopeMock.EXPECT(), clientMock.EXPECT(), asyncMock.EXPECT())

			s := &Service{
				Scope:      scopeMock,
				Client:     clientMock,
				Reconciler: asyncMock,
			}

			err := s.Delete(t.Context())
			if tc.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(strings.ReplaceAll(err.Error(), "\n", "")).To(MatchRegexp(tc.expectedError))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestIsManaged(t *testing.T) {
	g := NewWithT(t)
	t.Parallel()

	s := &Service{}
	managed, err := s.IsManaged(t.Context())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(managed).To(BeTrue())
}

// fakeHcpOpenShiftNodePool returns a fake NodePool, associated with fakeHcpOpenShiftNodePoolSpec.
func fakeHcpOpenShiftNodePool() arohcp.NodePool {
	diskStorageAccountType := arohcp.DiskStorageAccountTypePremiumLRS

	return arohcp.NodePool{
		Location: ptr.To("eastus"),
		Properties: &arohcp.NodePoolProperties{
			Platform: &arohcp.NodePoolPlatformProfile{
				VMSize:           ptr.To("Standard_D4s_v3"),
				AvailabilityZone: ptr.To("1"),
				OSDisk: &arohcp.OsDiskProfile{
					SizeGiB:                ptr.To[int32](127),
					DiskStorageAccountType: &diskStorageAccountType,
				},
				SubnetID: ptr.To("/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet"),
			},
			AutoRepair: ptr.To(true),
			Labels: []*arohcp.Label{
				{
					Key:   ptr.To("test-label"),
					Value: ptr.To("test-value"),
				},
			},
			Replicas: ptr.To[int32](3),
			Version: &arohcp.NodePoolVersionProfile{
				ChannelGroup: ptr.To("stable"),
				ID:           ptr.To("4.19.0"),
			},
		},
		Tags: map[string]*string{
			"test-key": ptr.To("test-value"),
		},
	}
}

// fakeHcpOpenShiftNodePoolSpec returns a fake spec for testing
func fakeHcpOpenShiftNodePoolSpec() *HcpOpenShiftNodePoolSpec {
	return &HcpOpenShiftNodePoolSpec{
		ClusterName:   fakeClusterName,
		Location:      "eastus",
		ResourceGroup: fakeGroupName,
		AROMachinePoolSpec: v1beta2.AROMachinePoolSpec{
			NodePoolName: fakeNodePoolName,
			Platform: v1beta2.AROPlatformProfileMachinePool{
				VMSize:                 "Standard_D4s_v3",
				DiskSizeGiB:            127,
				DiskStorageAccountType: "Premium_LRS",
				AvailabilityZone:       "1",
				Subnet:                 "/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.Network/virtualNetworks/test-vnet/subnets/test-subnet",
			},
			Version:    "4.19.0",
			AutoRepair: true,
			Labels: map[string]string{
				"test-label": "test-value",
			},
			AdditionalTags: map[string]string{
				"test-key": "test-value",
			},
		},
		AROControlPlaneSpec: cplane.AROControlPlaneSpec{
			ChannelGroup: "stable",
		},
		MachinePoolSpec: expv1.MachinePoolSpec{
			Replicas: ptr.To[int32](3),
		},
	}
}

// fakeExistingHcpOpenShiftNodePool returns a fake existing NodePool that matches the spec
func fakeExistingHcpOpenShiftNodePool() arohcp.NodePool {
	nodePool := fakeHcpOpenShiftNodePool()
	nodePool.ID = ptr.To("/subscriptions/test/resourceGroups/test-rg/providers/Microsoft.RedHatOpenShift/hcpOpenShiftClusters/test-cluster/nodePools/test-nodepool")
	return nodePool
}
