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
	"sigs.k8s.io/cluster-api-provider-azure/azure"
)

// HcpOpenShiftClusterCredentialsSpec defines the specification for a HcpOpenShiftCluster.
type HcpOpenShiftClusterCredentialsSpec struct {
	Name               string
	ResourceGroup      string
	APIURI             string
	HasValidKubeconfig bool
}

var _ azure.ResourceSpecGetter = &HcpOpenShiftClusterCredentialsSpec{}

// ResourceName returns the name of the HcpOpenShiftCluster.
func (s *HcpOpenShiftClusterCredentialsSpec) ResourceName() string {
	return s.Name
}

// ResourceGroupName returns the name of the resource group.
func (s *HcpOpenShiftClusterCredentialsSpec) ResourceGroupName() string {
	return s.ResourceGroup
}

// OwnerResourceName returns the cluster name.
func (s *HcpOpenShiftClusterCredentialsSpec) OwnerResourceName() string {
	return s.Name
}

func (s *HcpOpenShiftClusterCredentialsSpec) Parameters(ctx context.Context, existing interface{}) (params interface{}, err error) {
	return &HcpOpenShiftClusterCredentialsSpec{
		Name:               s.Name,
		ResourceGroup:      s.ResourceGroup,
		APIURI:             s.APIURI,
		HasValidKubeconfig: s.HasValidKubeconfig,
	}, nil
}
