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

package hcpopenshiftclustercredentials

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"

	infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	"sigs.k8s.io/cluster-api-provider-azure/azure"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/async"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/resourceskus"
	arohcp "sigs.k8s.io/cluster-api-provider-azure/exp/third_party/aro-hcp/api/v20240610preview/armredhatopenshifthcp"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
)

const serviceName = "hcpopenshiftclustercredentials"

type (
	// HcpOpenShiftClusterCredentialScope defines the scope interface for an HCP OpenShift cluster service.
	HcpOpenShiftClusterCredentialScope interface {
		azure.Authorizer
		azure.AsyncStatusUpdater
		HcpOpenShiftClusterCredentialsSpecs(context.Context) azure.ResourceSpecGetter
		SetKubeconfig(kubeconfig *string, timestamp *time.Time)
		ShouldReconcileKubeconfig(ctx context.Context) bool
		AnnotateKubeconfigInvalid(ctx context.Context) error
	}

	// Service provides operations on Azure resources.
	Service struct {
		Scope HcpOpenShiftClusterCredentialScope
		Client
		resourceSKUCache *resourceskus.Cache
		async.Reconciler
	}
)

// New creates a new service.
func New(scope HcpOpenShiftClusterCredentialScope, skuCache *resourceskus.Cache) (*Service, error) {
	client, err := newClient(scope, scope.DefaultedAzureCallTimeout())
	if err != nil {
		return nil, err
	}
	return &Service{
		Scope: scope,
		Reconciler: async.New[arohcp.HcpOpenShiftClustersClientRequestAdminCredentialResponse,
			arohcp.HcpOpenShiftClustersClientRevokeCredentialsResponse](scope, client, client),
		Client:           client,
		resourceSKUCache: skuCache,
	}, nil
}

// Name returns the service name.
func (s *Service) Name() string {
	return serviceName
}

// Reconcile idempotently gets, creates, and updates an HCP OpenShift cluster.
func (s *Service) Reconcile(ctx context.Context) error {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftclusters.Service.Reconcile")
	defer done()

	// HcpOpenShiftClustersReadyCondition is set in the VM service.
	ctx, cancel := context.WithTimeout(ctx, s.Scope.DefaultedAzureServiceReconcileTimeout())
	defer cancel()

	if err := s.validateSpec(ctx); err != nil {
		// do as much early validation as possible to limit calls to Azure
		return err
	}

	spec := s.Scope.HcpOpenShiftClusterCredentialsSpecs(ctx)
	hcpOpenShiftClusterCredentialsSpecs, ok := spec.(*HcpOpenShiftClusterCredentialsSpec)
	if !ok {
		return errors.Errorf("%T is not of type HcpOpenShiftClusterCredentialsSpecs", spec)
	}

	if hcpOpenShiftClusterCredentialsSpecs.APIURI == "" {
		return errors.Errorf("ARO ContrlolPlane not yet provisioned (Spec.APIURL is nil)")
	}

	if !s.Scope.ShouldReconcileKubeconfig(ctx) {
		return nil
	}

	result, err := s.CreateOrUpdateResource(ctx, hcpOpenShiftClusterCredentialsSpecs, serviceName)
	s.Scope.UpdatePutStatus(infrav1.BootstrapSucceededCondition, serviceName, err)

	if err == nil && result != nil {
		if err := s.updateScopeState(ctx, result, hcpOpenShiftClusterCredentialsSpecs); err != nil {
			return err
		}
	}

	return err
}

// updateScopeState updates the scope's hcpOpenShiftClusterAdminCredential state and provider ID
//
// Code later in the reconciler uses scope's hcpOpenShiftCluster state for determining HcpOpenShiftCluster status and whether to create/delete
// HcpOpenShiftClusterAdminCredential.
func (s *Service) updateScopeState(_ context.Context, result interface{}, _ *HcpOpenShiftClusterCredentialsSpec) error {
	hcpOpenShiftClusterAdminCredential, ok := result.(arohcp.HcpOpenShiftClusterAdminCredential)
	if !ok {
		return errors.Errorf("%T is not an arohcp.HcpOpenShiftCluster", result)
	}
	s.Scope.SetKubeconfig(hcpOpenShiftClusterAdminCredential.Kubeconfig, hcpOpenShiftClusterAdminCredential.ExpirationTimestamp)

	return nil
}

// Delete annotate kubeconfig
// The actual delete in Azure may take longer, but should eventually complete.
func (s *Service) Delete(ctx context.Context) error {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftclustercredentials.Service.Delete")
	defer done()
	err := s.Scope.AnnotateKubeconfigInvalid(ctx)
	if err != nil {
		log.Error(err, "failed to invalidate kube config")
	}
	// we don't need to revoke credentials before cluster's delete
	return nil
}

func (s *Service) validateSpec(ctx context.Context) error {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftclusters.Service.validateSpec")
	defer done()

	spec := s.Scope.HcpOpenShiftClusterCredentialsSpecs(ctx)
	hcpOpenShiftClusterCredentialsSpec, ok := spec.(*HcpOpenShiftClusterCredentialsSpec)
	if !ok {
		return errors.Errorf("%T is not a HcpOpenShiftClusterCredentialsSpecs", spec)
	}
	log.Info(fmt.Sprintf("validateSpec: %s", hcpOpenShiftClusterCredentialsSpec.Name))

	return nil
}

// IsManaged returns always returns true as CAPZ does not support BYO HcpOpenShiftCluster.
func (s *Service) IsManaged(_ context.Context) (bool, error) {
	return true, nil
}
