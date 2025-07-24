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
	"github.com/pkg/errors"
	infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/resourceskus"
	"time"

	arohcp "sigs.k8s.io/cluster-api-provider-azure/exp/third_party/aro-hcp/api/v20240610preview/generated"

	//infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	"sigs.k8s.io/cluster-api-provider-azure/azure"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/async"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
)

const serviceName = "hcpopenshiftclustercredentials"

type (
	// HcpOpenShiftClusterCredentialScope defines the scope interface for a hcpOpenShiftcluster service.
	HcpOpenShiftClusterCredentialScope interface {
		azure.Authorizer
		azure.AsyncStatusUpdater
		HcpOpenShiftClusterCredentialsSpecs(context.Context) azure.ResourceSpecGetter
		SetKubeconfig(kubeconfig *string, timestamp *time.Time)
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

// Reconcile idempotently gets, creates, and updates a hcpOpenShiftcluster.
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

	// is ready
	if hcpOpenShiftClusterCredentialsSpecs.HasValidKubeconfig {
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
func (s *Service) updateScopeState(ctx context.Context, result interface{}, hcpOpenShiftClusterSpecs *HcpOpenShiftClusterCredentialsSpec) error {
	hcpOpenShiftClusterAdminCredential, ok := result.(arohcp.HcpOpenShiftClusterAdminCredential)
	if !ok {
		return errors.Errorf("%T is not an arohcp.HcpOpenShiftCluster", result)
	}
	s.Scope.SetKubeconfig(hcpOpenShiftClusterAdminCredential.Kubeconfig, hcpOpenShiftClusterAdminCredential.ExpirationTimestamp)

	return nil
}

// Delete deletes a HcpOpenShiftCluster asynchronously. Delete sends a DELETE request to Azure and if accepted without error,
// The actual delete in Azure may take longer, but should eventually complete.
func (s *Service) Delete(ctx context.Context) error {
	return nil
	/* TODO: mveber - we don't need to revoke credentials before cluster's delete
	ctx, log, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftclusters.Service.Delete")
	defer done()

	ctx, cancel := context.WithTimeout(ctx, s.Scope.DefaultedAzureServiceReconcileTimeout())
	defer cancel()

	spec := s.Scope.HcpOpenShiftClusterCredentialsSpecs(ctx)
	hcpOpenShiftClusterSpecs, ok := spec.(*HcpOpenShiftClusterCredentialsSpec)
	if !ok {
		return errors.Errorf("%T is not a HcpOpenShiftClusterCredentialsSpecs", spec)
	}
	log.Info(fmt.Sprintf("Delete: %s", hcpOpenShiftClusterSpecs.Name))

	// We go through the list of HcpOpenShiftClustersSpecs to delete each one, independently of the result of the previous one.
	// If multiple errors occur, we return the most pressing one.
	//  Order of precedence (highest -> lowest) is: error that is not an operationNotDoneError (i.e. error creating) -> operationNotDoneError (i.e. creating in progress) -> no error (i.e. created)
	var result error
	if err := s.DeleteResource(ctx, hcpOpenShiftClusterSpecs, serviceName); err != nil {
		if !azure.IsOperationNotDoneError(err) || result == nil {
			result = err
		}
	}
	s.Scope.UpdateDeleteStatus(infrav1.BootstrapSucceededCondition, serviceName, result)
	return result
	*/
}

func (s *Service) validateSpec(ctx context.Context) error {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftclusters.Service.validateSpec")
	defer done()

	spec := s.Scope.HcpOpenShiftClusterCredentialsSpecs(ctx)
	hcpOpenShiftClusterSpecs, ok := spec.(*HcpOpenShiftClusterCredentialsSpec)
	if !ok {
		return errors.Errorf("%T is not a HcpOpenShiftClusterCredentialsSpecs", spec)
	}
	log.Info(fmt.Sprintf("validateSpec: %s", hcpOpenShiftClusterSpecs.Name))

	/* TODO: mveber - remove
	// Fetch location and zone to check for their support of ultra disks.
	zones, err := s.resourceSKUCache.GetZones(ctx, hcpOpenShiftClusterSpecs.Location)
	if err != nil {
		return azure.WithTerminalError(errors.Wrapf(err, "failed to get the zones for location %s", hcpOpenShiftClusterSpecs.Location))
	}

	for _, zone := range zones {
		hasLocationCapability := sku.HasLocationCapability(resourceskus.UltraSSDAvailable, hcpOpenShiftClusterSpecs.Location, zone)
		err := fmt.Errorf("vm size %s does not support ultra disks in location %s. select a different vm size or disable ultra disks", hcpOpenShiftClusterSpecs.Size, hcpOpenShiftClusterSpecs.Location)

		// Check support for ultra disks as data disks.
		for _, disks := range hcpOpenShiftClusterSpecs.DataDisks {
			if disks.ManagedDisk != nil &&
				disks.ManagedDisk.StorageAccountType == string(armcompute.StorageAccountTypesUltraSSDLRS) &&
				!hasLocationCapability {
				return azure.WithTerminalError(err)
			}
		}
		// Check support for ultra disks as persistent volumes.
		if hcpOpenShiftClusterSpecs.AdditionalCapabilities != nil && hcpOpenShiftClusterSpecs.AdditionalCapabilities.UltraSSDEnabled != nil {
			if *hcpOpenShiftClusterSpecs.AdditionalCapabilities.UltraSSDEnabled &&
				!hasLocationCapability {
				return azure.WithTerminalError(err)
			}
		}
	}

	// Validate DiagnosticProfile spec
	if hcpOpenShiftClusterSpecs.DiagnosticsProfile != nil && hcpOpenShiftClusterSpecs.DiagnosticsProfile.Boot != nil {
		if hcpOpenShiftClusterSpecs.DiagnosticsProfile.Boot.StorageAccountType == infrav1.UserManagedDiagnosticsStorage {
			if hcpOpenShiftClusterSpecs.DiagnosticsProfile.Boot.UserManaged == nil {
				return azure.WithTerminalError(fmt.Errorf("userManaged must be specified when storageAccountType is '%s'", infrav1.UserManagedDiagnosticsStorage))
			} else if hcpOpenShiftClusterSpecs.DiagnosticsProfile.Boot.UserManaged.StorageAccountURI == "" {
				return azure.WithTerminalError(fmt.Errorf("storageAccountURI cannot be empty when storageAccountType is '%s'", infrav1.UserManagedDiagnosticsStorage))
			}
		}

		possibleStorageAccountTypeValues := []string{
			string(infrav1.DisabledDiagnosticsStorage),
			string(infrav1.ManagedDiagnosticsStorage),
			string(infrav1.UserManagedDiagnosticsStorage),
		}

		if !slice.Contains(possibleStorageAccountTypeValues, string(hcpOpenShiftClusterSpecs.DiagnosticsProfile.Boot.StorageAccountType)) {
			return azure.WithTerminalError(fmt.Errorf("invalid storageAccountType: %s. Allowed values are %v",
				hcpOpenShiftClusterSpecs.DiagnosticsProfile.Boot.StorageAccountType, possibleStorageAccountTypeValues))
		}
	}

	// Checking if selected availability zones are available selected VM type in location
	azsInLocation, err := s.resourceSKUCache.GetZonesWithVMSize(ctx, hcpOpenShiftClusterSpecs.Size, hcpOpenShiftClusterSpecs.Location)
	if err != nil {
		return errors.Wrapf(err, "failed to get zones for VM type %s in location %s", hcpOpenShiftClusterSpecs.Size, hcpOpenShiftClusterSpecs.Location)
	}

	for _, az := range hcpOpenShiftClusterSpecs.FailureDomains {
		if !slice.Contains(azsInLocation, az) {
			return azure.WithTerminalError(errors.Errorf("availability zone %s is not available for VM type %s in location %s", az, hcpOpenShiftClusterSpecs.Size, hcpOpenShiftClusterSpecs.Location))
		}
	}
	*/

	return nil
}

// IsManaged returns always returns true as CAPZ does not support BYO HcpOpenShiftCluster.
func (s *Service) IsManaged(_ context.Context) (bool, error) {
	return true, nil
}
