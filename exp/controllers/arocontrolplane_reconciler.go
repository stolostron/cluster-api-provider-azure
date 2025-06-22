/*
Copyright 2019 The Kubernetes Authors.

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

package controllers

import (
	"context"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/hcpopenshiftclusters"

	"github.com/pkg/errors"

	"sigs.k8s.io/cluster-api-provider-azure/azure"
	"sigs.k8s.io/cluster-api-provider-azure/azure/scope"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/resourceskus"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
)

// aroControlPlaneService is the reconciler called by the AROControlPlane controller.
type aroControlPlaneService struct {
	scope *scope.AROControlPlaneScope
	// services is the list of services that are reconciled by this controller.
	// The order of the services is important as it determines the order in which the services are reconciled.
	services  []azure.ServiceReconciler
	skuCache  *resourceskus.Cache
	Reconcile func(context.Context) error
	Pause     func(context.Context) error
	Delete    func(context.Context) error
}

// newAROControlPlaneService populates all the services based on input scope.
func newAROControlPlaneService(scope *scope.AROControlPlaneScope) (*aroControlPlaneService, error) {
	skuCache, err := resourceskus.GetCache(scope, scope.Location())
	if err != nil {
		return nil, errors.Wrap(err, "failed creating a NewCache")
	}
	hpcOpenshiftSvc, err := hcpopenshiftclusters.New(scope, skuCache)
	if err != nil {
		return nil, err
	}
	acs := &aroControlPlaneService{
		scope: scope,
		services: []azure.ServiceReconciler{
			hpcOpenshiftSvc,
		},
		skuCache: skuCache,
	}
	acs.Reconcile = acs.reconcile
	acs.Pause = acs.pause
	acs.Delete = acs.delete

	return acs, nil
}

// Reconcile reconciles all the services in a predetermined order.
func (s *aroControlPlaneService) reconcile(ctx context.Context) error {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "controllers.aroControlPlaneService.Reconcile")
	defer done()

	/*
		if err := s.setFailureDomainsForLocation(ctx); err != nil {
			return errors.Wrap(err, "failed to get availability zones")
		}
		if s.scope.ControlPlaneEnabled() {
			s.scope.AROControlPlane.SetBackendPoolNameDefault()
			s.scope.SetDNSName()
			s.scope.SetControlPlaneSecurityRules()
		}
	*/

	for _, service := range s.services {
		if err := service.Reconcile(ctx); err != nil {
			return errors.Wrapf(err, "failed to reconcile AROControlPlane service %s", service.Name())
		}
	}

	return nil
}

// Pause pauses all components making up the cluster.
func (s *aroControlPlaneService) pause(ctx context.Context) error {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "controllers.aroControlPlaneService.Pause")
	defer done()

	for _, service := range s.services {
		pauser, ok := service.(azure.Pauser)
		if !ok {
			continue
		}
		if err := pauser.Pause(ctx); err != nil {
			return errors.Wrapf(err, "failed to pause AROControlPlane service %s", service.Name())
		}
	}

	return nil
}

// Delete reconciles all the services in a predetermined order.
func (s *aroControlPlaneService) delete(ctx context.Context) error {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "controllers.aroControlPlaneService.Delete")
	defer done()

	// If the resource group is not managed we need to delete resources inside the group one by one.
	// services are deleted in reverse order from the order in which they are reconciled.
	for i := len(s.services) - 1; i >= 0; i-- {
		if err := s.services[i].Delete(ctx); err != nil {
			return errors.Wrapf(err, "failed to delete AROControlPlane service %s", s.services[i].Name())
		}
	}

	return nil
}

/*
// setFailureDomainsForLocation sets the AROControlPlane Status failure domains based on which Azure Availability Zones are available in the cluster location.
// Note that this is not done in a webhook as it requires API calls to fetch the availability zones.
func (s *aroControlPlaneService) setFailureDomainsForLocation(ctx context.Context) error {
	if s.scope.ExtendedLocation() != nil {
		return nil
	}

	zones, err := s.skuCache.GetZones(ctx, s.scope.Location())
	if err != nil {
		return errors.Wrapf(err, "failed to get zones for location %s", s.scope.Location())
	}

	for _, zone := range zones {
		s.scope.SetFailureDomain(zone, clusterv1.FailureDomainSpec{
			ControlPlane: true,
		})
	}

	return nil
}
*/

func (s *aroControlPlaneService) getService(name string) (azure.ServiceReconciler, error) {
	for _, service := range s.services {
		if service.Name() == name {
			return service, nil
		}
	}
	return nil, errors.Errorf("service %s not found", name)
}
