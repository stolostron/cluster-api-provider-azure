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
	infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"

	arohcp "github.com/marek-veber/ARO-HCP/external/api/v20240610preview/generated"

	//infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	"sigs.k8s.io/cluster-api-provider-azure/azure"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/async"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
)

const serviceName = "hcpopenshiftclusters"

// HcpOpenShiftClusterScope defines the scope interface for a hcpOpenShiftcluster service.
type HcpOpenShiftClusterScope interface {
	azure.ClusterDescriber
	azure.AsyncStatusUpdater
	HcpOpenShiftClusterSpecs() []azure.ResourceSpecGetter
}

// Service provides operations on Azure resources.
type Service struct {
	Scope HcpOpenShiftClusterScope
	async.Reconciler
}

// New creates a new service.
func New(scope HcpOpenShiftClusterScope) (*Service, error) {
	client, err := newClient(scope, scope.DefaultedAzureCallTimeout())
	if err != nil {
		return nil, err
	}
	return &Service{
		Scope: scope,
		Reconciler: async.New[arohcp.HcpOpenShiftClustersClientCreateOrUpdateResponse,
			arohcp.HcpOpenShiftClustersClientDeleteResponse](scope, nil, client),
	}, nil
}

// Name returns the service name.
func (s *Service) Name() string {
	return serviceName
}

// Reconcile idempotently creates or updates an hcpOpenShiftcluster.
func (s *Service) Reconcile(ctx context.Context) error {
	_, _, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftclusters.Service.Reconcile")
	defer done()

	// HcpOpenShiftClustersReadyCondition is set in the VM service.
	return nil
}

// Delete deletes the HcpOpenShiftCluster associated with a VM.
func (s *Service) Delete(ctx context.Context) error {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftclusters.Service.Delete")
	defer done()

	ctx, cancel := context.WithTimeout(ctx, s.Scope.DefaultedAzureServiceReconcileTimeout())
	defer cancel()

	specs := s.Scope.HcpOpenShiftClusterSpecs()
	if len(specs) == 0 {
		return nil
	}

	// We go through the list of HcpOpenShiftClustersSpecs to delete each one, independently of the result of the previous one.
	// If multiple errors occur, we return the most pressing one.
	//  Order of precedence (highest -> lowest) is: error that is not an operationNotDoneError (i.e. error creating) -> operationNotDoneError (i.e. creating in progress) -> no error (i.e. created)
	var result error
	for _, hcpOpenShiftClustersSpec := range specs {
		if err := s.DeleteResource(ctx, hcpOpenShiftClustersSpec, serviceName); err != nil {
			if !azure.IsOperationNotDoneError(err) || result == nil {
				result = err
			}
		}
	}
	s.Scope.UpdateDeleteStatus(infrav1.VnetPeeringReadyCondition, serviceName, result)
	return result
}

// IsManaged returns always returns true as CAPZ does not support BYO HcpOpenShiftCluster.
func (s *Service) IsManaged(_ context.Context) (bool, error) {
	return true, nil
}
