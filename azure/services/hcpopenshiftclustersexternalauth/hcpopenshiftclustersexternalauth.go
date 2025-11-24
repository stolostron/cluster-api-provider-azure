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

// Package hcpopenshiftclustersexternalauth provides ASO-based HCP OpenShift cluster external authentication management.
package hcpopenshiftclustersexternalauth

import (
	"context"
	"fmt"

	asoredhatopenshiftv1 "github.com/Azure/azure-service-operator/v2/api/redhatopenshift/v1api20240610preview"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/cluster-api-provider-azure/azure/scope"
	cplane "sigs.k8s.io/cluster-api-provider-azure/exp/api/controlplane/v1beta2"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
)

const serviceName = "hcpopenshiftclustersexternalauth"

// Service provides ASO-based operations on HCP OpenShift cluster external authentication.
type Service struct {
	Scope  *scope.AROControlPlaneScope
	client client.Client
}

// New creates a new ASO-based HCP OpenShift cluster external auth service.
func New(aroScope *scope.AROControlPlaneScope) (*Service, error) {
	return &Service{
		Scope:  aroScope,
		client: aroScope.Client,
	}, nil
}

// Name returns the service name.
func (s *Service) Name() string {
	return serviceName
}

// Reconcile creates or updates the HcpOpenShiftClustersExternalAuth ASO resources.
func (s *Service) Reconcile(ctx context.Context) error {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftclustersexternalauth.Service.Reconcile")
	defer done()

	// Skip if external auth is not enabled
	if !s.Scope.ControlPlane.Spec.EnableExternalAuthProviders {
		log.V(4).Info("external auth providers not enabled, skipping reconciliation")
		return nil
	}

	log.V(4).Info("reconciling HcpOpenShiftClustersExternalAuth with ASO")

	// Reconcile each external auth provider
	for _, provider := range s.Scope.ControlPlane.Spec.ExternalAuthProviders {
		externalAuth, err := s.buildHcpOpenShiftClustersExternalAuth(ctx, provider)
		if err != nil {
			return errors.Wrapf(err, "failed to build HcpOpenShiftClustersExternalAuth for provider %s", provider.Name)
		}

		log.V(4).Info("applying HcpOpenShiftClustersExternalAuth ASO resource", "name", externalAuth.Name, "provider", provider.Name)

		// Apply the HcpOpenShiftClustersExternalAuth using server-side apply
		err = s.client.Patch(ctx, externalAuth, client.Apply, client.FieldOwner("capz-manager"), client.ForceOwnership)
		if err != nil {
			return errors.Wrapf(err, "failed to apply HcpOpenShiftClustersExternalAuth for provider %s", provider.Name)
		}
	}

	return nil
}

// Delete removes the HcpOpenShiftClustersExternalAuth ASO resources.
func (s *Service) Delete(ctx context.Context) error {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftclustersexternalauth.Service.Delete")
	defer done()

	log.V(4).Info("deleting HcpOpenShiftClustersExternalAuth resources")

	// Delete each external auth provider
	for _, provider := range s.Scope.ControlPlane.Spec.ExternalAuthProviders {
		externalAuth := &asoredhatopenshiftv1.HcpOpenShiftClustersExternalAuth{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%s", s.Scope.ClusterName(), provider.Name),
				Namespace: s.Scope.ControlPlane.Namespace,
			},
		}

		err := s.client.Delete(ctx, externalAuth)
		if err != nil && client.IgnoreNotFound(err) != nil {
			return errors.Wrapf(err, "failed to delete HcpOpenShiftClustersExternalAuth for provider %s", provider.Name)
		}
	}

	return nil
}

// IsManaged returns true if external auth is enabled.
func (s *Service) IsManaged(ctx context.Context) (bool, error) {
	return s.Scope.ControlPlane.Spec.EnableExternalAuthProviders, nil
}

// Pause is a no-op for external auth.
func (s *Service) Pause(ctx context.Context) error {
	return nil
}

// buildHcpOpenShiftClustersExternalAuth creates the ASO HcpOpenShiftClustersExternalAuth resource from CAPI types.
func (s *Service) buildHcpOpenShiftClustersExternalAuth(ctx context.Context, provider cplane.ExternalAuthProvider) (*asoredhatopenshiftv1.HcpOpenShiftClustersExternalAuth, error) {
	// Build the external auth properties
	properties := &asoredhatopenshiftv1.ExternalAuthProperties{
		Issuer: &asoredhatopenshiftv1.TokenIssuerProfile{
			Url:       ptr.To(provider.Issuer.URL),
			Audiences: []string{},
		},
	}

	// Convert audiences
	for _, aud := range provider.Issuer.Audiences {
		properties.Issuer.Audiences = append(properties.Issuer.Audiences, string(aud))
	}

	// Set CA if provided
	if provider.Issuer.CertificateAuthority != nil {
		// TODO: Fetch the CA bundle from the referenced configmap
		// For now, we'll leave it nil as it's optional
		properties.Issuer.Ca = nil
	}

	// Convert claim mappings
	if provider.ClaimMappings != nil {
		properties.Claim = &asoredhatopenshiftv1.ExternalAuthClaimProfile{
			Mappings: &asoredhatopenshiftv1.TokenClaimMappingsProfile{},
		}

		// Username mapping
		if provider.ClaimMappings.Username != nil {
			usernameProfile := &asoredhatopenshiftv1.UsernameClaimProfile{
				Claim: ptr.To(provider.ClaimMappings.Username.Claim),
			}

			// Map prefix policy
			switch provider.ClaimMappings.Username.PrefixPolicy {
			case cplane.NoPrefix:
				usernameProfile.PrefixPolicy = ptr.To(asoredhatopenshiftv1.UsernameClaimPrefixPolicy_NoPrefix)
			case cplane.Prefix:
				usernameProfile.PrefixPolicy = ptr.To(asoredhatopenshiftv1.UsernameClaimPrefixPolicy_Prefix)
				usernameProfile.Prefix = provider.ClaimMappings.Username.Prefix
			default:
				usernameProfile.PrefixPolicy = ptr.To(asoredhatopenshiftv1.UsernameClaimPrefixPolicy_None)
			}

			properties.Claim.Mappings.Username = usernameProfile
		}

		// Groups mapping
		if provider.ClaimMappings.Groups != nil {
			properties.Claim.Mappings.Groups = &asoredhatopenshiftv1.GroupClaimProfile{
				Claim:  ptr.To(provider.ClaimMappings.Groups.Claim),
				Prefix: ptr.To(provider.ClaimMappings.Groups.Prefix),
			}
		}

		// Validation rules
		if len(provider.ClaimValidationRules) > 0 {
			properties.Claim.ValidationRules = []asoredhatopenshiftv1.TokenClaimValidationRule{}
			for _, rule := range provider.ClaimValidationRules {
				properties.Claim.ValidationRules = append(properties.Claim.ValidationRules, asoredhatopenshiftv1.TokenClaimValidationRule{
					Type: ptr.To(asoredhatopenshiftv1.TokenClaimValidationRule_Type_RequiredClaim),
					RequiredClaim: &asoredhatopenshiftv1.TokenRequiredClaim{
						Claim:         ptr.To(rule.RequiredClaim.Claim),
						RequiredValue: ptr.To(rule.RequiredClaim.RequiredValue),
					},
				})
			}
		}
	}

	// Convert OIDC clients
	if len(provider.OIDCClients) > 0 {
		properties.Clients = []asoredhatopenshiftv1.ExternalAuthClientProfile{}
		for _, oidcClient := range provider.OIDCClients {
			clientProfile := asoredhatopenshiftv1.ExternalAuthClientProfile{
				ClientId: ptr.To(oidcClient.ClientID),
				Component: &asoredhatopenshiftv1.ExternalAuthClientComponentProfile{
					Name:                ptr.To(oidcClient.ComponentName),
					AuthClientNamespace: ptr.To(oidcClient.ComponentNamespace),
				},
				Type: ptr.To(asoredhatopenshiftv1.ExternalAuthClientType_Confidential),
			}

			if len(oidcClient.ExtraScopes) > 0 {
				clientProfile.ExtraScopes = oidcClient.ExtraScopes
			}

			properties.Clients = append(properties.Clients, clientProfile)
		}
	}

	// Create the HcpOpenShiftClustersExternalAuth resource
	externalAuth := &asoredhatopenshiftv1.HcpOpenShiftClustersExternalAuth{
		TypeMeta: metav1.TypeMeta{
			APIVersion: asoredhatopenshiftv1.GroupVersion.String(),
			Kind:       "HcpOpenShiftClustersExternalAuth",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", s.Scope.ClusterName(), provider.Name),
			Namespace: s.Scope.ControlPlane.Namespace,
			Labels: map[string]string{
				"cluster.x-k8s.io/cluster-name": s.Scope.ClusterName(),
			},
		},
		Spec: asoredhatopenshiftv1.HcpOpenShiftClustersExternalAuth_Spec{
			AzureName: provider.Name,
			Owner: &genruntime.KnownResourceReference{
				Name: s.Scope.ClusterName(),
			},
			Properties: properties,
		},
	}

	return externalAuth, nil
}
