/*
Copyright 2018 The Kubernetes Authors.

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

package scope

import (
	"context"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	expv1 "sigs.k8s.io/cluster-api/exp/api/v1beta1"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/controller-runtime/pkg/client"

	infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	"sigs.k8s.io/cluster-api-provider-azure/azure"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/hcpopenshiftnodepools"
	cplane "sigs.k8s.io/cluster-api-provider-azure/exp/api/controlplane/v1beta2"
	v1beta2 "sigs.k8s.io/cluster-api-provider-azure/exp/api/v1beta2"
	arohcp "sigs.k8s.io/cluster-api-provider-azure/exp/third_party/aro-hcp/api/v20240610preview/generated"
	"sigs.k8s.io/cluster-api-provider-azure/util/futures"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
)

// AROMachinePoolScopeParams defines the input parameters used to create a new Scope.
type AROMachinePoolScopeParams struct {
	AzureClients
	Client               client.Client
	Cluster              *clusterv1.Cluster
	MachinePool          *expv1.MachinePool
	ControlPlane         *cplane.AROControlPlane
	AROMachinePool       *v1beta2.AROMachinePool
	Cache                *AROMachinePoolCache
	Timeouts             azure.AsyncReconciler
	CredentialCache      azure.CredentialCache
	AROControlPlaneScope *AROControlPlaneScope
}

// NewAROMachinePoolScope creates a new Scope from the supplied parameters.
// This is meant to be called for each reconcile iteration.
func NewAROMachinePoolScope(ctx context.Context, params AROMachinePoolScopeParams) (*AROMachinePoolScope, error) {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "azure.aroMachinePoolScope.NewAROMachinePoolScope")
	defer done()

	if params.AROMachinePool == nil {
		return nil, errors.New("failed to generate new scope from nil AROMachinePool")
	}

	credentialsProvider, err := NewAzureCredentialsProvider(ctx, params.CredentialCache, params.Client, params.ControlPlane.Spec.IdentityRef, params.AROMachinePool.Namespace)
	if err != nil {
		return nil, errors.Wrap(err, "failed to init credentials provider")
	}
	err = params.AzureClients.setCredentialsWithProvider(ctx, params.ControlPlane.Spec.SubscriptionID, params.ControlPlane.Spec.AzureEnvironment, credentialsProvider)
	if err != nil {
		return nil, errors.Wrap(err, "failed to configure azure settings and credentials for Identity")
	}

	if params.Cache == nil {
		params.Cache = &AROMachinePoolCache{}
	}

	helper, err := patch.NewHelper(params.AROMachinePool, params.Client)
	if err != nil {
		return nil, errors.Errorf("failed to init patch helper: %v", err)
	}

	capiMachinePoolPatchHelper, err := patch.NewHelper(params.MachinePool, params.Client)
	if err != nil {
		return nil, errors.Wrap(err, "failed to init patch helper")
	}
	return &AROMachinePoolScope{
		Client:                     params.Client,
		patchHelper:                helper,
		cache:                      params.Cache,
		AzureClients:               params.AzureClients,
		Cluster:                    params.Cluster,
		MachinePool:                params.MachinePool,
		ControlPlane:               params.ControlPlane,
		InfraMachinePool:           params.AROMachinePool,
		capiMachinePoolPatchHelper: capiMachinePoolPatchHelper,
		AsyncReconciler:            params.Timeouts,
	}, nil
}

// AROMachinePoolScope defines the basic context for an actuator to operate upon.
type AROMachinePoolScope struct {
	Client                     client.Client
	patchHelper                *patch.Helper
	capiMachinePoolPatchHelper *patch.Helper
	cache                      *AROMachinePoolCache

	AzureClients
	Cluster          *clusterv1.Cluster
	ControlPlane     *cplane.AROControlPlane
	MachinePool      *expv1.MachinePool
	InfraMachinePool *v1beta2.AROMachinePool

	azure.AsyncReconciler
}

// NodePoolSpecs returns the resource spec getter for node pools.
func (s *AROMachinePoolScope) NodePoolSpecs(_ context.Context) azure.ResourceSpecGetter {
	ret := &hcpopenshiftnodepools.HcpOpenShiftNodePoolSpec{
		ClusterName:         s.ClusterName(),
		Location:            s.Location(),
		ResourceGroup:       s.ResourceGroup(),
		AROMachinePoolSpec:  s.InfraMachinePool.Spec,
		AROControlPlaneSpec: s.ControlPlane.Spec,
		MachinePoolSpec:     s.MachinePool.Spec,
	}
	return ret
}

// SetStatusVersion sets the version profile in the machine pool status.
func (s *AROMachinePoolScope) SetStatusVersion(versionProfile *arohcp.NodePoolVersionProfile) {
	if versionProfile == nil {
		return
	}
	s.InfraMachinePool.Status.Version = *versionProfile.ID
}

// SetProvisioningState sets the provisioning state in the machine pool status.
func (s *AROMachinePoolScope) SetProvisioningState(state *arohcp.ProvisioningState) {
	if state == nil {
		conditions.MarkUnknown(s.InfraMachinePool, v1beta2.AROMachinePoolReadyCondition, infrav1.CreatingReason, "nil ProvisioningState was returned")
		return
	}
	s.InfraMachinePool.Status.ProvisioningState = string(*state)
	if *state == arohcp.ProvisioningStateSucceeded {
		conditions.MarkTrue(s.InfraMachinePool, v1beta2.AROMachinePoolReadyCondition)
		return
	}
	reason := infrav1.CreatingReason
	if *state == arohcp.ProvisioningStateUpdating {
		reason = infrav1.UpdatingReason
	}
	conditions.MarkFalse(s.InfraMachinePool, v1beta2.AROMachinePoolReadyCondition, reason, clusterv1.ConditionSeverityInfo, "ProvisioningState=%s", string(*state))
}

// SetLongRunningOperationState will set the future on the AROMachinePool status to allow the resource to continue
// in the next reconciliation.
func (s *AROMachinePoolScope) SetLongRunningOperationState(future *infrav1.Future) {
	futures.Set(s.InfraMachinePool, future)
}

// GetLongRunningOperationState will get the future on the AROMachinePool status.
func (s *AROMachinePoolScope) GetLongRunningOperationState(name, service, futureType string) *infrav1.Future {
	return futures.Get(s.InfraMachinePool, name, service, futureType)
}

// DeleteLongRunningOperationState will delete the future from the AROMachinePool status.
func (s *AROMachinePoolScope) DeleteLongRunningOperationState(name, service, futureType string) {
	futures.Delete(s.InfraMachinePool, name, service, futureType)
}

// UpdateDeleteStatus updates a condition on the AROMachinePool status after a DELETE operation.
func (s *AROMachinePoolScope) UpdateDeleteStatus(condition clusterv1.ConditionType, service string, err error) {
	switch {
	case err == nil:
		conditions.MarkFalse(s.InfraMachinePool, condition, infrav1.DeletedReason, clusterv1.ConditionSeverityInfo, "%s successfully deleted", service)
	case azure.IsOperationNotDoneError(err):
		conditions.MarkFalse(s.InfraMachinePool, condition, infrav1.DeletingReason, clusterv1.ConditionSeverityInfo, "%s deleting", service)
	default:
		conditions.MarkFalse(s.InfraMachinePool, condition, infrav1.DeletionFailedReason, clusterv1.ConditionSeverityError, "%s failed to delete. err: %s", service, err.Error())
	}
}

// UpdatePutStatus updates a condition on the AROMachinePool status after a PUT operation.
func (s *AROMachinePoolScope) UpdatePutStatus(condition clusterv1.ConditionType, service string, err error) {
	switch {
	case err == nil:
		conditions.MarkTrue(s.InfraMachinePool, condition)
	case azure.IsOperationNotDoneError(err):
		reason := infrav1.CreatingReason
		if s.InfraMachinePool.Status.ProvisioningState == string(arohcp.ProvisioningStateUpdating) {
			reason = infrav1.UpdatingReason
		}
		conditions.MarkFalse(s.InfraMachinePool, condition, reason, clusterv1.ConditionSeverityInfo, "%s creating or updating", service)
	default:
		conditions.MarkFalse(s.InfraMachinePool, condition, infrav1.FailedReason, clusterv1.ConditionSeverityError, "%s failed to create or update. err: %s", service, err.Error())
	}
}

// UpdatePatchStatus updates a condition on the AROMachinePool status after a PATCH operation.
func (s *AROMachinePoolScope) UpdatePatchStatus(condition clusterv1.ConditionType, service string, err error) {
	switch {
	case err == nil:
		conditions.MarkTrue(s.InfraMachinePool, condition)
	case azure.IsOperationNotDoneError(err):
		conditions.MarkFalse(s.InfraMachinePool, condition, infrav1.UpdatingReason, clusterv1.ConditionSeverityInfo, "%s updating", service)
	default:
		conditions.MarkFalse(s.InfraMachinePool, condition, infrav1.FailedReason, clusterv1.ConditionSeverityError, "%s failed to update. err: %s", service, err.Error())
	}
}

// AROMachinePoolCache stores AROMachinePoolCache data locally so we don't have to hit the API multiple times within the same reconcile loop.
type AROMachinePoolCache struct {
}

// BaseURI returns the Azure ResourceManagerEndpoint.
func (s *AROMachinePoolScope) BaseURI() string {
	return s.ResourceManagerEndpoint
}

// GetClient returns the controller-runtime client.
func (s *AROMachinePoolScope) GetClient() client.Client {
	return s.Client
}

// GetDeletionTimestamp returns the deletion timestamp of the Cluster.
func (s *AROMachinePoolScope) GetDeletionTimestamp() *metav1.Time {
	return s.Cluster.DeletionTimestamp
}

// PatchObject persists the control plane configuration and status.
func (s *AROMachinePoolScope) PatchObject(ctx context.Context) error {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "scope.ManagedMachinePoolScope.PatchObject")
	defer done()

	conditions.SetSummary(s.InfraMachinePool)

	return s.patchHelper.Patch(
		ctx,
		s.InfraMachinePool,
		patch.WithOwnedConditions{Conditions: []clusterv1.ConditionType{
			clusterv1.ReadyCondition,
			v1beta2.AROMachinePoolReadyCondition,
			// v1beta2.AROMachinePoolValidCondition,
			// v1beta2.AROMachinePoolUpgradingCondition,
		}})
}

// PatchCAPIMachinePoolObject persists the capi machinepool configuration and status.
func (s *AROMachinePoolScope) PatchCAPIMachinePoolObject(ctx context.Context) error {
	return s.capiMachinePoolPatchHelper.Patch(
		ctx,
		s.MachinePool,
	)
}

// SetAgentPoolProviderIDList sets a list of agent pool's Azure VM IDs.
func (s *AROMachinePoolScope) SetAgentPoolProviderIDList(providerIDs []string) {
	s.InfraMachinePool.Spec.ProviderIDList = providerIDs
}

// SetAgentPoolReplicas sets the number of agent pool replicas.
func (s *AROMachinePoolScope) SetAgentPoolReplicas(replicas int32) {
	s.InfraMachinePool.Status.Replicas = replicas
}

// SetAgentPoolReady sets the flag that indicates if the agent pool is ready or not.
func (s *AROMachinePoolScope) SetAgentPoolReady(ready bool) {
	if s.InfraMachinePool.Status.ProvisioningState != string(arohcp.ProvisioningStateSucceeded) &&
		s.InfraMachinePool.Status.ProvisioningState != string(arohcp.ProvisioningStateUpdating) {
		ready = false
	}
	s.InfraMachinePool.Status.Ready = ready
	if s.InfraMachinePool.Status.Initialization == nil || !s.InfraMachinePool.Status.Initialization.Provisioned {
		s.InfraMachinePool.Status.Initialization = &v1beta2.AROMachinePoolInitializationStatus{Provisioned: ready}
	}
}

// Close closes the current scope persisting the control plane configuration and status.
func (s *AROMachinePoolScope) Close(ctx context.Context) error {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "scope.AROMachinePoolScope.Close")
	defer done()

	return s.PatchObject(ctx)
}

// Name returns the machine pool name.
func (s *AROMachinePoolScope) Name() string {
	return s.InfraMachinePool.Name
}

// Location returns location.
func (s *AROMachinePoolScope) Location() string {
	return s.ControlPlane.Spec.Platform.Location
}

// ResourceGroup returns the cluster resource group.
func (s *AROMachinePoolScope) ResourceGroup() string {
	return s.ControlPlane.Spec.Platform.ResourceGroup
}

// NodeResourceGroup returns the node resource group name.
func (s *AROMachinePoolScope) NodeResourceGroup() string {
	return s.ControlPlane.NodeResourceGroup()
}

// ClusterName returns the cluster name.
func (s *AROMachinePoolScope) ClusterName() string {
	return s.Cluster.Name
}

// Namespace returns the cluster namespace.
func (s *AROMachinePoolScope) Namespace() string {
	return s.Cluster.Namespace
}
