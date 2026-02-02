/*
Copyright 2020 The Kubernetes Authors.

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
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v5"
	asoredhatopenshiftv1 "github.com/Azure/azure-service-operator/v2/api/redhatopenshift/v1api20240610preview"
	asoconditions "github.com/Azure/azure-service-operator/v2/pkg/genruntime/conditions"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	clusterv1beta1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/conditions"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"sigs.k8s.io/cluster-api-provider-azure/azure"
	"sigs.k8s.io/cluster-api-provider-azure/azure/scope"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/hcpopenshiftnodepools"
	"sigs.k8s.io/cluster-api-provider-azure/azure/services/virtualmachines"
	"sigs.k8s.io/cluster-api-provider-azure/controllers"
	infrav1exp "sigs.k8s.io/cluster-api-provider-azure/exp/api/v1beta2"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/mutators"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
)

type (
	// aroMachinePoolService contains the services required by the cluster controller.
	aroMachinePoolService struct {
		scope                 *scope.AROMachinePoolScope
		agentPoolsSvc         azure.Reconciler
		virtualMachinesSvc    NodeLister
		kubeclient            client.Client
		newResourceReconciler func(*infrav1exp.AROMachinePool, []*unstructured.Unstructured) resourceReconciler
	}

	// AgentPoolVMSSNotFoundError represents a reconcile error when the VMSS for an agent pool can't be found.
	AgentPoolVMSSNotFoundError struct {
		NodeResourceGroup string
		PoolName          string
	}

	// NodeLister is a service interface for returning generic lists.
	NodeLister interface {
		List(context.Context, string) ([]armcompute.VirtualMachine, error)
	}
)

// NewAgentPoolVMSSNotFoundError creates a new AgentPoolVMSSNotFoundError.
func NewAgentPoolVMSSNotFoundError(nodeResourceGroup, poolName string) *AgentPoolVMSSNotFoundError {
	return &AgentPoolVMSSNotFoundError{
		NodeResourceGroup: nodeResourceGroup,
		PoolName:          poolName,
	}
}

func (a *AgentPoolVMSSNotFoundError) Error() string {
	msgFmt := "failed to find vm scale set in resource group %s matching pool named %s"
	return fmt.Sprintf(msgFmt, a.NodeResourceGroup, a.PoolName)
}

// Is returns true if the target error is an `AgentPoolVMSSNotFoundError`.
func (a *AgentPoolVMSSNotFoundError) Is(target error) bool {
	var err *AgentPoolVMSSNotFoundError
	ok := errors.As(target, &err)
	return ok
}

// newAROMachinePoolService populates all the services based on input scope.
func newAROMachinePoolService(scope *scope.AROMachinePoolScope, apiCallTimeout time.Duration) (*aroMachinePoolService, error) {
	virtualMachinesAuthorizer, err := virtualMachinesAuthorizer(scope)
	if err != nil {
		return nil, err
	}
	virtualMachinesClient, err := virtualmachines.NewClient(virtualMachinesAuthorizer, apiCallTimeout)
	if err != nil {
		return nil, err
	}
	nodePoolService, err := hcpopenshiftnodepools.New(scope)
	if err != nil {
		return nil, err
	}
	return &aroMachinePoolService{
		scope:              scope,
		agentPoolsSvc:      nodePoolService,
		virtualMachinesSvc: virtualMachinesClient,
		kubeclient:         scope.Client,
		newResourceReconciler: func(machinePool *infrav1exp.AROMachinePool, resources []*unstructured.Unstructured) resourceReconciler {
			return controllers.NewResourceReconciler(scope.Client, resources, machinePool)
		},
	}, nil
}

// virtualMachinesAuthorizer takes a scope and determines if a regional authorizer is needed for scale sets
// see https://github.com/kubernetes-sigs/cluster-api-provider-azure/pull/1850 for context on region based authorizer.
func virtualMachinesAuthorizer(scope *scope.AROMachinePoolScope) (azure.Authorizer, error) {
	/* TODO: mveber - why/how
	if scope.ControlPlane.Spec.AzureEnvironment == azure.PublicCloudName {
		return azure.WithRegionalBaseURI(scope, scope.Location()) // public cloud supports regional end points
	}
	*/

	return scope, nil
}

// Reconcile reconciles all the services in a predetermined order.
func (s *aroMachinePoolService) Reconcile(ctx context.Context) error {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "controllers.aroMachinePoolService.Reconcile")
	defer done()

	log.Info("reconciling ARO machine pool")

	// Check if we're using resources mode (new approach)
	if len(s.scope.InfraMachinePool.Spec.Resources) > 0 {
		log.V(4).Info("Using resources mode for AROMachinePool reconciliation")
		return s.reconcileResources(ctx)
	}

	// Legacy mode: use field-based services
	log.V(4).Info("Using field-based mode for AROMachinePool reconciliation")

	if s.scope.InfraMachinePool.Spec.Autoscaling != nil && !annotations.ReplicasManagedByExternalAutoscaler(s.scope.MachinePool) {
		// make sure cluster.x-k8s.io/replicas-managed-by annotation is set on CAPI MachinePool when autoscaling is enabled.
		annotations.AddAnnotations(s.scope.MachinePool, map[string]string{
			clusterv1beta1.ReplicasManagedByAnnotation: "aro",
		})
	}

	agentPoolName := s.scope.Name()

	if err := s.agentPoolsSvc.Reconcile(ctx); err != nil {
		return errors.Wrapf(err, "failed to reconcile ARO machine pool %s", agentPoolName)
	}

	nodeResourceGroup := s.scope.NodeResourceGroup()
	vmss, err := s.virtualMachinesSvc.List(ctx, nodeResourceGroup)
	if err != nil {
		return errors.Wrapf(err, "failed to list vmss in resource group %s", nodeResourceGroup)
	}

	namePrefix := s.scope.ClusterName() + "-" + s.scope.InfraMachinePool.Spec.NodePoolName + "-"
	var providerIDs []string
	for _, vm := range vmss {
		if vm.Name == nil || !strings.HasPrefix(*vm.Name, namePrefix) {
			continue
		}
		if vm.ID == nil {
			continue
		}
		providerIDs = append(providerIDs, "azure://"+*vm.ID)
	}
	currentReplicas := int32(len(providerIDs))

	if annotations.ReplicasManagedByExternalAutoscaler(s.scope.MachinePool) {
		// Set MachinePool replicas to aro autoscaling replicas
		if *s.scope.MachinePool.Spec.Replicas != currentReplicas {
			log.Info("Setting MachinePool replicas to aro autoscaling replicas",
				"local", *s.scope.MachinePool.Spec.Replicas,
				"external", currentReplicas)
			s.scope.MachinePool.Spec.Replicas = &currentReplicas
		}
	}

	s.scope.SetAgentPoolProviderIDList(providerIDs)
	s.scope.SetAgentPoolReplicas(currentReplicas)
	s.scope.SetAgentPoolReady(true)

	log.Info("reconciled ARO machine pool successfully")
	return nil
}

// Pause pauses all components making up the machine pool.
func (s *aroMachinePoolService) Pause(ctx context.Context) error {
	ctx, _, done := tele.StartSpanWithLogger(ctx, "controllers.aroMachinePoolService.Pause")
	defer done()

	pauser, ok := s.agentPoolsSvc.(azure.Pauser)
	if !ok {
		return nil
	}
	if err := pauser.Pause(ctx); err != nil {
		return errors.Wrapf(err, "failed to pause machine pool %s", s.scope.Name())
	}

	return nil
}

// Delete reconciles all the services in a predetermined order.
func (s *aroMachinePoolService) Delete(ctx context.Context) error {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "controllers.aroMachinePoolService.Delete")
	defer done()

	// Check if we're using resources mode (new approach)
	if len(s.scope.InfraMachinePool.Spec.Resources) > 0 {
		log.V(4).Info("Using resources mode for AROMachinePool deletion")
		return s.deleteResources(ctx)
	}

	// Legacy mode: delete field-based services
	log.V(4).Info("Using field-based mode for AROMachinePool deletion")
	if err := s.agentPoolsSvc.Delete(ctx); err != nil {
		return errors.Wrapf(err, "failed to delete machine pool %s", s.scope.Name())
	}

	return nil
}

// reconcileResources handles reconciliation when spec.resources is specified.
func (s *aroMachinePoolService) reconcileResources(ctx context.Context) error {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "controllers.aroMachinePoolService.reconcileResources")
	defer done()

	log.V(4).Info("Reconciling AROMachinePool using resources mode")

	// Get HCP cluster name from the control plane
	// This is needed to set owner references for the node pool
	hcpClusterName := s.getHcpClusterName()

	// Apply mutators to set defaults and owner references
	resources, err := mutators.ApplyMutators(
		ctx,
		s.scope.InfraMachinePool.Spec.Resources,
		mutators.SetHcpOpenShiftNodePoolDefaults(s.kubeclient, s.scope.InfraMachinePool, hcpClusterName),
	)
	if err != nil {
		return errors.Wrap(err, "failed to apply mutators")
	}

	// Use the ResourceReconciler to apply resources
	resourceReconciler := s.newResourceReconciler(s.scope.InfraMachinePool, resources)

	if err := resourceReconciler.Reconcile(ctx); err != nil {
		return errors.Wrap(err, "failed to reconcile ASO resources")
	}

	// Find HcpOpenShiftClustersNodePool to extract status information
	var nodePoolName string
	for _, resource := range resources {
		if resource.GroupVersionKind().Group == asoredhatopenshiftv1.GroupVersion.Group &&
			resource.GroupVersionKind().Kind == "HcpOpenShiftClustersNodePool" {
			nodePoolName = resource.GetName()
			break
		}
	}

	if nodePoolName == "" {
		return errors.New("no HcpOpenShiftClustersNodePool found in resources")
	}

	// Get the HcpOpenShiftClustersNodePool to extract status
	nodePool := &asoredhatopenshiftv1.HcpOpenShiftClustersNodePool{}
	if err := s.kubeclient.Get(ctx, client.ObjectKey{
		Namespace: s.scope.InfraMachinePool.Namespace,
		Name:      nodePoolName,
	}, nodePool); err != nil {
		return errors.Wrap(err, "failed to get HcpOpenShiftNodePool")
	}

	// Extract status information from HcpOpenShiftNodePool
	if nodePool.Status.Id != nil {
		s.scope.InfraMachinePool.Status.ID = *nodePool.Status.Id
	}

	if nodePool.Status.Properties != nil && nodePool.Status.Properties.Version != nil && nodePool.Status.Properties.Version.Id != nil {
		s.scope.InfraMachinePool.Status.Version = *nodePool.Status.Properties.Version.Id
	}

	if nodePool.Status.Properties != nil && nodePool.Status.Properties.ProvisioningState != nil {
		s.scope.InfraMachinePool.Status.ProvisioningState = string(*nodePool.Status.Properties.ProvisioningState)
	}

	// Set replicas from node pool status
	// For HCP node pools with autoscaling, the status doesn't include replicas count
	// In that case, use the CAPI MachinePool replicas as the source of truth
	if nodePool.Status.Properties != nil && nodePool.Status.Properties.Replicas != nil {
		replicas := int32(*nodePool.Status.Properties.Replicas)
		s.scope.SetAgentPoolReplicas(replicas)
	} else if s.scope.MachinePool.Spec.Replicas != nil {
		s.scope.SetAgentPoolReplicas(*s.scope.MachinePool.Spec.Replicas)
	}

	// Mark as ready and set condition based on HcpOpenShiftClustersNodePool status
	ready := false
	var readyCondition *asoconditions.Condition
	for i, condition := range nodePool.Status.Conditions {
		if condition.Type == asoconditions.ConditionTypeReady {
			readyCondition = &nodePool.Status.Conditions[i]
			if condition.Status == metav1.ConditionTrue {
				ready = true
			}
			break
		}
	}

	// Set the NodePoolReady condition based on the HcpOpenShiftClustersNodePool status
	if ready {
		conditions.Set(s.scope.InfraMachinePool, metav1.Condition{
			Type:   string(infrav1exp.NodePoolReadyCondition),
			Status: metav1.ConditionTrue,
			Reason: "Succeeded",
		})
	} else {
		// Extract error details from HcpOpenShiftClustersNodePool's Ready condition
		reason := "Provisioning"
		message := "HcpOpenShiftClustersNodePool is not yet ready"

		if readyCondition != nil {
			if readyCondition.Reason != "" {
				reason = readyCondition.Reason
			}
			if readyCondition.Message != "" {
				message = readyCondition.Message
			}

			// If there's an error or warning severity, prepend it to the message for visibility
			if readyCondition.Severity == asoconditions.ConditionSeverityError || readyCondition.Severity == asoconditions.ConditionSeverityWarning {
				message = fmt.Sprintf("[%s] %s", readyCondition.Severity, message)
			}
		}

		conditions.Set(s.scope.InfraMachinePool, metav1.Condition{
			Type:    string(infrav1exp.NodePoolReadyCondition),
			Status:  metav1.ConditionFalse,
			Reason:  reason,
			Message: message,
		})
	}

	s.scope.SetAgentPoolReady(ready)

	// Set the top-level AROMachinePoolReadyCondition based on overall status
	// This is the condition that clusterctl uses to display machine pool status
	if ready {
		conditions.Set(s.scope.InfraMachinePool, metav1.Condition{
			Type:   string(infrav1exp.AROMachinePoolReadyCondition),
			Status: metav1.ConditionTrue,
			Reason: "Succeeded",
		})
	} else {
		// Extract error details from NodePoolReady condition to propagate to top-level condition
		reason := "Provisioning"
		message := "ARO machine pool is not yet ready"

		if readyCondition != nil {
			if readyCondition.Reason != "" {
				reason = readyCondition.Reason
			}
			if readyCondition.Message != "" {
				message = readyCondition.Message
			}

			if readyCondition.Severity == asoconditions.ConditionSeverityError || readyCondition.Severity == asoconditions.ConditionSeverityWarning {
				message = fmt.Sprintf("[%s] %s", readyCondition.Severity, message)
			}
		}

		conditions.Set(s.scope.InfraMachinePool, metav1.Condition{
			Type:    string(infrav1exp.AROMachinePoolReadyCondition),
			Status:  metav1.ConditionFalse,
			Reason:  reason,
			Message: message,
		})
	}

	// Check if all resources are ready
	allResourcesReady := true
	for _, status := range s.scope.InfraMachinePool.Status.Resources {
		if !status.Ready {
			allResourcesReady = false
			log.V(4).Info("waiting for resource to be ready", "resource", status.Resource.Name)
			break
		}
	}

	// Set initialization provisioned status for CAPI contract
	// Infrastructure is provisioned when all resources are ready
	if allResourcesReady && len(s.scope.InfraMachinePool.Status.Resources) > 0 {
		s.scope.InfraMachinePool.Status.Initialization = &infrav1exp.AROMachinePoolInitializationStatus{
			Provisioned: true,
		}
	} else if s.scope.InfraMachinePool.Status.Initialization == nil {
		s.scope.InfraMachinePool.Status.Initialization = &infrav1exp.AROMachinePoolInitializationStatus{
			Provisioned: false,
		}
	}

	// Return early if resources aren't ready to allow continued reconciliation
	if !allResourcesReady {
		return nil
	}

	log.V(4).Info("successfully reconciled AROMachinePool using resources mode")
	return nil
}

// deleteResources handles deletion when spec.resources is specified.
func (s *aroMachinePoolService) deleteResources(ctx context.Context) error {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "controllers.aroMachinePoolService.deleteResources")
	defer done()

	log.V(4).Info("Deleting AROMachinePool using resources mode")

	// Use the ResourceReconciler to delete resources
	// Pass nil for resources to indicate all should be deleted
	resourceReconciler := s.newResourceReconciler(s.scope.InfraMachinePool, nil)

	if err := resourceReconciler.Delete(ctx); err != nil {
		return errors.Wrap(err, "failed to delete ASO resources")
	}

	// Check if there are still resources being deleted
	// The ResourceReconciler updates the status with resources that are still deleting
	for _, status := range s.scope.InfraMachinePool.Status.Resources {
		if !status.Ready {
			log.V(4).Info("waiting for resource to be deleted", "resource", status.Resource.Name)
			return azure.WithTransientError(errors.New("waiting for resources to be deleted"), 15*time.Second)
		}
	}

	return nil
}

// getHcpClusterName retrieves the HCP cluster name from the control plane.
func (s *aroMachinePoolService) getHcpClusterName() string {
	// The HCP cluster name should match the CAPI cluster name
	// This is consistent with how AROControlPlane names its HcpOpenShiftCluster
	return s.scope.ClusterName()
}
