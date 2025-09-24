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
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/pkg/errors"

	"sigs.k8s.io/cluster-api-provider-azure/azure"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/async"
	arohcp "sigs.k8s.io/cluster-api-provider-azure/exp/third_party/aro-hcp/api/v20240610preview/armredhatopenshifthcp"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
)

// Client wraps go-sdk.
type Client interface {
	Get(context.Context, azure.ResourceSpecGetter) (interface{}, error)
	List(context.Context, string) ([]arohcp.HcpOpenShiftCluster, error)

	CreateOrUpdateAsync(ctx context.Context, spec azure.ResourceSpecGetter, resumeToken string, parameters interface{}) (result interface{}, poller *runtime.Poller[arohcp.HcpOpenShiftClustersClientCreateOrUpdateResponse], err error)
	DeleteAsync(ctx context.Context, spec azure.ResourceSpecGetter, resumeToken string) (poller *runtime.Poller[arohcp.HcpOpenShiftClustersClientDeleteResponse], err error)
}

// azureClient contains the Azure go-sdk Client.
type azureClient struct {
	hcpopenshiftcluster *arohcp.HcpOpenShiftClustersClient
	apiCallTimeout      time.Duration
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

// Get gets the specified HcpOpenShiftCluster.
func (ac *azureClient) Get(ctx context.Context, spec azure.ResourceSpecGetter) (result interface{}, err error) {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftclusters.azureClient.Get")
	defer done()

	// Get(ctx context.Context, resourceGroupName string, hcpOpenShiftClusterName string, options *HcpOpenShiftClustersClientGetOptions)
	resp, err := ac.hcpopenshiftcluster.Get(ctx, spec.ResourceGroupName(), spec.ResourceName(), nil)
	if err != nil {
		return nil, err
	}
	return resp.HcpOpenShiftCluster, nil
}

// List returns all HcpOpenShiftClusters.
func (ac *azureClient) List(ctx context.Context, resourceGroupName string) (result []arohcp.HcpOpenShiftCluster, err error) {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftclusters.azureClient.List")
	defer done()

	var openShiftClusters []arohcp.HcpOpenShiftCluster
	if resourceGroupName != "" {
		pager := ac.hcpopenshiftcluster.NewListByResourceGroupPager(resourceGroupName, nil)
		for pager.More() {
			nextResult, err := pager.NextPage(ctx)
			if err != nil {
				return openShiftClusters, errors.Wrap(err, "could not iterate HcpOpenShiftClusters by resourceGroupName")
			}
			for _, natRule := range nextResult.Value {
				openShiftClusters = append(openShiftClusters, *natRule)
			}
		}
	} else {
		pager := ac.hcpopenshiftcluster.NewListBySubscriptionPager(nil)
		for pager.More() {
			nextResult, err := pager.NextPage(ctx)
			if err != nil {
				return openShiftClusters, errors.Wrap(err, "could not iterate HcpOpenShiftClusters by subscription")
			}
			for _, natRule := range nextResult.Value {
				openShiftClusters = append(openShiftClusters, *natRule)
			}
		}
	}

	return openShiftClusters, nil
}

// CreateOrUpdateAsync creates or updates an HcpOpenShiftCluster asynchronously.
// It sends a PUT request to Azure and if accepted without error, the func will return a Poller which can be used to track the ongoing
// progress of the operation.
func (ac *azureClient) CreateOrUpdateAsync(ctx context.Context, spec azure.ResourceSpecGetter, resumeToken string, parameters interface{}) (result interface{}, poller *runtime.Poller[arohcp.HcpOpenShiftClustersClientCreateOrUpdateResponse], err error) {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "hcpopenshiftclusters.azureClient.CreateOrUpdateAsync")
	defer done()

	hcpOpenShiftCluster, ok := parameters.(arohcp.HcpOpenShiftCluster)
	if !ok && parameters != nil {
		return nil, nil, errors.Errorf("%T is not an arohcp.HcpOpenShiftCluster", parameters)
	}

	opts := &arohcp.HcpOpenShiftClustersClientBeginCreateOrUpdateOptions{ResumeToken: resumeToken}
	log.V(4).Info("sending request", "resumeToken", resumeToken)
	poller, err = ac.hcpopenshiftcluster.BeginCreateOrUpdate(ctx, spec.ResourceGroupName(), spec.ResourceName(), hcpOpenShiftCluster, opts)
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
	return resp.HcpOpenShiftCluster, nil, err
}

// DeleteAsync deletes a AROCluster asynchronously. DeleteAsync sends a DELETE
// request to Azure and if accepted without error, the func will return a Poller which can be used to track the ongoing
// progress of the operation.
func (ac *azureClient) DeleteAsync(ctx context.Context, spec azure.ResourceSpecGetter, resumeToken string) (poller *runtime.Poller[arohcp.HcpOpenShiftClustersClientDeleteResponse], err error) {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "aro.azureClient.DeleteAsync")
	defer done()

	opts := &arohcp.HcpOpenShiftClustersClientBeginDeleteOptions{ResumeToken: resumeToken}
	log.V(4).Info("sending request", "resumeToken", resumeToken)
	poller, err = ac.hcpopenshiftcluster.BeginDelete(ctx, spec.ResourceGroupName(), spec.ResourceName(), opts)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, ac.apiCallTimeout)
	defer cancel()

	pollOpts := &runtime.PollUntilDoneOptions{Frequency: async.DefaultPollerFrequency}
	_, err = poller.PollUntilDone(ctx, pollOpts)
	if err != nil {
		// if an error occurs, return the Poller.
		// this means the long-running operation didn't finish in the specified timeout.
		return poller, err
	}
	// if the operation completed, return a nil poller.
	return nil, err
}
