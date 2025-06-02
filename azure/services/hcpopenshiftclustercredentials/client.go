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
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/pkg/errors"
	cloudprovider "k8s.io/cloud-provider"

	"sigs.k8s.io/cluster-api-provider-azure/azure"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/async"
	arohcp "sigs.k8s.io/cluster-api-provider-azure/exp/third_party/aro-hcp/api/v20240610preview/generated"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
)

// Client wraps go-sdk.
type Client interface {
	CreateOrUpdateAsync(ctx context.Context, spec azure.ResourceSpecGetter, resumeToken string, parameters interface{}) (result interface{}, poller *runtime.Poller[arohcp.HcpOpenShiftClustersClientRequestAdminCredentialResponse], err error)
	DeleteAsync(ctx context.Context, spec azure.ResourceSpecGetter, resumeToken string) (poller *runtime.Poller[arohcp.HcpOpenShiftClustersClientRevokeCredentialsResponse], err error)
}

// azureClient contains the Azure go-sdk Client.
type azureClient struct {
	hcpopenshiftcluster *arohcp.HcpOpenShiftClustersClient
	apiCallTimeout      time.Duration
}

func (ac *azureClient) Get(_ context.Context, _ azure.ResourceSpecGetter) (result interface{}, err error) {
	return nil, runtime.NewResponseErrorWithErrorCode(&http.Response{StatusCode: http.StatusNotFound}, cloudprovider.InstanceNotFound.Error())
}

var _ Client = &azureClient{}

// newClient creates a new AROCluster client from an authorizer.
func newClient(auth azure.Authorizer, apiCallTimeout time.Duration) (*azureClient, error) {
	isDevel := false
	var extraPolicies []policy.Policy
	if isDevel {
		now := time.Now()
		extraPolicies = append(extraPolicies, azure.CustomPutPatchHeaderPolicy{
			Headers: map[string]string{
				"X-Ms-Arm-Resource-System-Data": fmt.Sprintf(`{"createdBy": "mveber", "createdByType": "User", "createdAt": %q}`,
					now.Format(time.RFC3339),
				),
				"X-Ms-Identity-Url": "https://dummyhost.identity.azure.net",
			},
		})
	}

	opts, err := azure.ARMClientOptions(auth.CloudEnvironment(), extraPolicies...)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create hcpopenshiftclusters client options")
	}
	cred := auth.Token()
	if isDevel {
		opts.InsecureAllowCredentialWithHTTP = true
		opts.Cloud.Services = map[cloud.ServiceName]cloud.ServiceConfiguration{
			"resourceManager": {
				Audience: opts.Cloud.Services["resourceManager"].Audience,
				Endpoint: "http://192.168.122.1:8443/",
			},
		}
	}
	factory, err := arohcp.NewClientFactory(auth.SubscriptionID(), cred, opts)
	if err != nil {
		return nil, errors.Wrap(err, "failed to create hcpopenshiftclusters client factory")
	}
	return &azureClient{factory.NewHcpOpenShiftClustersClient(), apiCallTimeout}, nil
}

// CreateOrUpdateAsync calls RequestAdminCredentialAsync request temporary credentials asynchronously.
func (ac *azureClient) CreateOrUpdateAsync(ctx context.Context, spec azure.ResourceSpecGetter, resumeToken string, _ interface{}) (result interface{}, poller *runtime.Poller[arohcp.HcpOpenShiftClustersClientRequestAdminCredentialResponse], err error) {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftclusters.azureClient.CreateOrUpdateAsync")
	defer done()

	opts := &arohcp.HcpOpenShiftClustersClientBeginRequestAdminCredentialOptions{ResumeToken: resumeToken}
	log.V(4).Info("sending request", "resumeToken", resumeToken)
	poller, err = ac.hcpopenshiftcluster.BeginRequestAdminCredential(ctx, spec.ResourceGroupName(), spec.ResourceName(), opts)
	if err != nil {
		return nil, nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, ac.apiCallTimeout)
	defer cancel()

	pollOpts := &runtime.PollUntilDoneOptions{Frequency: async.DefaultPollerFrequency}
	resp, err := poller.PollUntilDone(ctx, pollOpts)
	if err != nil {
		// If an error occurs, return the poller.
		// This means the long-running operation didn't finish in the specified timeout.
		return nil, poller, err
	}

	// if the operation completed, return a nil poller
	return resp.HcpOpenShiftClusterAdminCredential, nil, err
}

// DeleteAsync calls RevokeCredentialsAsync revokes temporary credentials asynchronously.
func (ac *azureClient) DeleteAsync(ctx context.Context, spec azure.ResourceSpecGetter, resumeToken string) (poller *runtime.Poller[arohcp.HcpOpenShiftClustersClientRevokeCredentialsResponse], err error) {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftclusters.azureClient.CreateOrUpdateAsync")
	defer done()

	opts := &arohcp.HcpOpenShiftClustersClientBeginRevokeCredentialsOptions{ResumeToken: resumeToken}
	log.V(4).Info("sending request", "resumeToken", resumeToken)
	poller, err = ac.hcpopenshiftcluster.BeginRevokeCredentials(ctx, spec.ResourceGroupName(), spec.ResourceName(), opts)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, ac.apiCallTimeout)
	defer cancel()

	pollOpts := &runtime.PollUntilDoneOptions{Frequency: async.DefaultPollerFrequency}
	_, err = poller.PollUntilDone(ctx, pollOpts)
	if err != nil {
		// If an error occurs, return the poller.
		// This means the long-running operation didn't finish in the specified timeout.
		return poller, err
	}

	// if the operation completed, return a nil poller
	return nil, err
}
