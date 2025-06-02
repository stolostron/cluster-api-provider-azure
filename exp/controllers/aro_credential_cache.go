/*
Copyright 2024 The Kubernetes Authors.

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
	"strconv"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/tracing/azotel"
	asoannotations "github.com/Azure/azure-service-operator/v2/pkg/common/annotations"
	"github.com/Azure/azure-service-operator/v2/pkg/common/config"
	"go.opentelemetry.io/otel"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/cluster-api-provider-azure/azure"
	"sigs.k8s.io/cluster-api-provider-azure/azure/scope"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
)

const (
	aroNamespaceSecretName = "aro-credential" //nolint:gosec // This is not a secret, only a reference to one.
	aroGlobalSecretName    = "aro-controller-settings"
	aroNamespaceAnnotation = "serviceoperator.azure.com/operator-namespace"
)

// AROCredentialCache caches credentials defined for ARO resources.
type AROCredentialCache interface {
	authTokenForAROResource(context.Context, client.Object) (azcore.TokenCredential, error)
}

type aroCredentialCache struct {
	cache  azure.CredentialCache
	client client.Client
}

// NewAROCredentialCache creates a new AROCredentialCache.
func NewAROCredentialCache(cache azure.CredentialCache, client client.Client) AROCredentialCache {
	return &aroCredentialCache{
		cache:  cache,
		client: client,
	}
}

func (c *aroCredentialCache) authTokenForAROResource(ctx context.Context, obj client.Object) (azcore.TokenCredential, error) {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "controllers.aroCredentialCache.authTokenForAROResource")
	defer done()

	clientOpts, err := c.clientOptsForAROResource(ctx, obj)
	if err != nil {
		return nil, err
	}

	secretName := aroNamespaceSecretName
	if resourceSecretName := obj.GetAnnotations()[asoannotations.PerResourceSecret]; resourceSecretName != "" {
		secretName = resourceSecretName
	}
	secret := &corev1.Secret{}
	err = c.client.Get(ctx, client.ObjectKey{Namespace: obj.GetNamespace(), Name: secretName}, secret)
	if client.IgnoreNotFound(err) != nil {
		return nil, err
	}
	if err == nil {
		return c.authTokenForScopedAROSecret(secret, clientOpts)
	}

	secretNamespace := obj.GetAnnotations()[aroNamespaceAnnotation]
	err = c.client.Get(ctx, client.ObjectKey{Namespace: secretNamespace, Name: aroGlobalSecretName}, secret)
	if err != nil {
		return nil, err
	}

	return c.authTokenForGlobalAROSecret(secret, clientOpts)
}

func (c *aroCredentialCache) clientOptsForAROResource(ctx context.Context, obj client.Object) (azcore.ClientOptions, error) {
	secretNamespace := obj.GetAnnotations()[aroNamespaceAnnotation]
	secret := &corev1.Secret{}
	err := c.client.Get(ctx, client.ObjectKey{Namespace: secretNamespace, Name: aroGlobalSecretName}, secret)
	if client.IgnoreNotFound(err) != nil {
		return azcore.ClientOptions{}, err
	}

	opts := azcore.ClientOptions{
		TracingProvider: azotel.NewTracingProvider(otel.GetTracerProvider(), nil),
		Cloud: cloud.Configuration{
			ActiveDirectoryAuthorityHost: string(secret.Data[config.AzureAuthorityHost]),
		},
	}

	if len(secret.Data[config.ResourceManagerAudience]) > 0 ||
		len(secret.Data[config.ResourceManagerEndpoint]) > 0 {
		opts.Cloud.Services = map[cloud.ServiceName]cloud.ServiceConfiguration{
			cloud.ResourceManager: {
				Audience: string(secret.Data[config.ResourceManagerAudience]),
				Endpoint: string(secret.Data[config.ResourceManagerEndpoint]),
			},
		}
	}

	return opts, nil
}

func (c *aroCredentialCache) authTokenForScopedAROSecret(secret *corev1.Secret, clientOpts azcore.ClientOptions) (azcore.TokenCredential, error) {
	d := secret.Data

	if _, hasSecret := d[config.AzureClientSecret]; hasSecret {
		return c.cache.GetOrStoreClientSecret(
			string(d[config.AzureTenantID]),
			string(d[config.AzureClientID]),
			string(d[config.AzureClientSecret]),
			&azidentity.ClientSecretCredentialOptions{
				ClientOptions: clientOpts,
			},
		)
	}

	if _, hasCert := d[config.AzureClientCertificate]; hasCert {
		return c.cache.GetOrStoreClientCert(
			string(d[config.AzureTenantID]),
			string(d[config.AzureClientID]),
			d[config.AzureClientCertificate],
			d[config.AzureClientCertificatePassword],
			&azidentity.ClientCertificateCredentialOptions{
				ClientOptions: clientOpts,
			},
		)
	}

	if authMode := d[config.AuthMode]; config.AuthModeOption(authMode) == config.PodIdentityAuthMode {
		return c.cache.GetOrStoreManagedIdentity(
			&azidentity.ManagedIdentityCredentialOptions{
				ClientOptions: clientOpts,
				ID:            azidentity.ClientID(d[config.AzureClientID]),
			},
		)
	}

	return c.cache.GetOrStoreWorkloadIdentity(
		&azidentity.WorkloadIdentityCredentialOptions{
			ClientOptions: clientOpts,
			TenantID:      string(d[config.AzureTenantID]),
			ClientID:      string(d[config.AzureClientID]),
			TokenFilePath: scope.GetProjectedTokenPath(),
		},
	)
}

func (c *aroCredentialCache) authTokenForGlobalAROSecret(secret *corev1.Secret, clientOpts azcore.ClientOptions) (azcore.TokenCredential, error) {
	d := secret.Data

	if workloadID, _ := strconv.ParseBool(string(d[config.UseWorkloadIdentityAuth])); workloadID {
		return c.cache.GetOrStoreWorkloadIdentity(
			&azidentity.WorkloadIdentityCredentialOptions{
				ClientOptions: clientOpts,
				TenantID:      string(d[config.AzureTenantID]),
				ClientID:      string(d[config.AzureClientID]),
				TokenFilePath: scope.GetProjectedTokenPath(),
			},
		)
	}

	if _, hasSecret := d[config.AzureClientSecret]; hasSecret {
		return c.cache.GetOrStoreClientSecret(
			string(d[config.AzureTenantID]),
			string(d[config.AzureClientID]),
			string(d[config.AzureClientSecret]),
			&azidentity.ClientSecretCredentialOptions{
				ClientOptions: clientOpts,
			},
		)
	}

	if _, hasCert := d[config.AzureClientCertificate]; hasCert {
		return c.cache.GetOrStoreClientCert(
			string(d[config.AzureTenantID]),
			string(d[config.AzureClientID]),
			d[config.AzureClientCertificate],
			d[config.AzureClientCertificatePassword],
			&azidentity.ClientCertificateCredentialOptions{
				ClientOptions: clientOpts,
			},
		)
	}

	return c.cache.GetOrStoreManagedIdentity(
		&azidentity.ManagedIdentityCredentialOptions{
			ClientOptions: clientOpts,
			ID:            azidentity.ClientID(d[config.AzureClientID]),
		},
	)
}
