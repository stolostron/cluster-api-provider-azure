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
	arohcp "sigs.k8s.io/cluster-api-provider-azure/exp/third_party/aro-hcp/api/v20240610preview/generated"
	"github.com/pkg/errors"
	infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"

	//infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	"sigs.k8s.io/cluster-api-provider-azure/azure"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/async"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
)

const serviceName = "hcpopenshiftclusters"

type (
	// HcpOpenShiftNodePoolScope defines the scope interface for a hcpOpenShiftcluster service.
	HcpOpenShiftNodePoolScope interface {
		azure.Authorizer
		azure.AsyncStatusUpdater
		NodePoolSpecs(context.Context) azure.ResourceSpecGetter
		SetProvisioningState(state *arohcp.ProvisioningState)
		SetStatusVersion(version *arohcp.NodePoolVersionProfile)
	}

	// Service provides operations on Azure resources.
	Service struct {
		Scope HcpOpenShiftNodePoolScope
		Client
		async.Reconciler
	}
)

// New creates a new service.
func New(scope HcpOpenShiftNodePoolScope) (*Service, error) {
	client, err := newClient(scope, scope.DefaultedAzureCallTimeout())
	if err != nil {
		return nil, err
	}
	return &Service{
		Scope: scope,
		Reconciler: async.New[arohcp.NodePoolsClientCreateOrUpdateResponse,
			arohcp.NodePoolsClientDeleteResponse](scope, client, client),
		Client: client,
	}, nil
}

// Name returns the service name.
func (s *Service) Name() string {
	return serviceName
}

// Reconcile idempotently gets, creates, and updates a NodePool.
func (s *Service) Reconcile(ctx context.Context) error {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftnodepools.Service.Reconcile")
	defer done()

	// ReadyCondition is set in the VM service.
	ctx, cancel := context.WithTimeout(ctx, s.Scope.DefaultedAzureServiceReconcileTimeout())
	defer cancel()

	if err := s.validateSpec(ctx); err != nil {
		// do as much early validation as possible to limit calls to Azure
		return err
	}

	spec := s.Scope.NodePoolSpecs(ctx)
	nodePoolSpecs, ok := spec.(*HcpOpenShiftNodePoolSpec)
	if !ok {
		return errors.Errorf("%T is not of type HcpOpenShiftNodePoolSpec", spec)
	}

	result, err := s.Client.Get(ctx, spec)
	if err == nil {
		if result != nil {
			if err := s.updateScopeState(ctx, result, nodePoolSpecs); err != nil {
				return err
			}
		}
	} else if !azure.ResourceNotFound(err) {
		return errors.Wrapf(err, "failed to get existing NodePool")
	}

	result, err = s.CreateOrUpdateResource(ctx, nodePoolSpecs, serviceName)
	s.Scope.UpdatePutStatus(infrav1.BootstrapSucceededCondition, serviceName, err)

	if err == nil && result != nil {
		if err := s.updateScopeState(ctx, result, nodePoolSpecs); err != nil {
			return err
		}
	}

	return err
}

// updateScopeState updates the scope's NodePool state and provider ID
//
// Code later in the reconciler uses scope's HcpOpenShiftNodePoolScope state for determining NodePool status and whether to create/delete
// NodePool.
func (s *Service) updateScopeState(ctx context.Context, result interface{}, nodePoolSpec *HcpOpenShiftNodePoolSpec) error {
	nodePool, ok := result.(arohcp.NodePool)
	if !ok {
		return errors.Errorf("%T is not an arohcp.NodePool", result)
	}
	s.Scope.SetProvisioningState(nodePool.Properties.ProvisioningState)
	s.Scope.SetStatusVersion(nodePool.Properties.Version)

	return nil
}

// Delete deletes a NodePool asynchronously. Delete sends a DELETE request to Azure and if accepted without error,
// The actual delete in Azure may take longer, but should eventually complete.
func (s *Service) Delete(ctx context.Context) error {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftnodepools.Service.Delete")
	defer done()

	ctx, cancel := context.WithTimeout(ctx, s.Scope.DefaultedAzureServiceReconcileTimeout())
	defer cancel()

	spec := s.Scope.NodePoolSpecs(ctx)
	nodePoolSpecs, ok := spec.(*HcpOpenShiftNodePoolSpec)
	if !ok {
		return errors.Errorf("%T is not a HcpOpenShiftNodePoolSpec", spec)
	}
	log.Info(fmt.Sprintf("Delete: %s", nodePoolSpecs.ClusterName))

	// We go through the list of nodePollSpecs to delete each one, independently of the result of the previous one.
	// If multiple errors occur, we return the most pressing one.
	//  Order of precedence (highest -> lowest) is: error that is not an operationNotDoneError (i.e. error creating) -> operationNotDoneError (i.e. creating in progress) -> no error (i.e. created)
	var result error
	if err := s.DeleteResource(ctx, nodePoolSpecs, serviceName); err != nil {
		if !azure.IsOperationNotDoneError(err) || result == nil {
			result = err
		}
	}
	s.Scope.UpdateDeleteStatus(infrav1.BootstrapSucceededCondition, serviceName, result)
	return result
}

func (s *Service) validateSpec(ctx context.Context) error {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftnodepools.Service.validateSpec")
	defer done()

	spec := s.Scope.NodePoolSpecs(ctx)
	nodePoolSpecSpecs, ok := spec.(*HcpOpenShiftNodePoolSpec)
	if !ok {
		return errors.Errorf("%T is not a HcpOpenShiftNodePoolSpec", spec)
	}
	log.Info(fmt.Sprintf("validateSpec: %s", nodePoolSpecSpecs.ClusterName))

	return nil
}

// IsManaged returns always returns true as CAPZ does not support BYO NodePool.
func (s *Service) IsManaged(_ context.Context) (bool, error) {
	return true, nil
}
