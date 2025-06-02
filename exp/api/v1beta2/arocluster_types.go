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

package v1beta2

import (
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	infrav1 "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

// AROClusterSpec defines the desired state of AROCluster.
type AROClusterSpec struct {
	// ControlPlaneEndpoint represents the endpoint used to communicate with the control plane.
	// +optional
	ControlPlaneEndpoint clusterv1.APIEndpoint `json:"controlPlaneEndpoint"`

	// TODO mveber - added
	//FailureDomains map[string]any

	// Secrets: configures where to place Azure generated secrets.
	Secrets *ManagedClusterOperatorSecrets `json:"secrets,omitempty"`
}

// ManagedClusterOperatorSecrets
// TODO: mveber - added
type ManagedClusterOperatorSecrets struct {
	// AdminCredentials: indicates where the AdminCredentials secret should be placed. If omitted, the secret will not be
	// retrieved from Azure.
	AdminCredentials *genruntime.SecretDestination `json:"adminCredentials,omitempty"`

	// UserCredentials: indicates where the UserCredentials secret should be placed. If omitted, the secret will not be
	// retrieved from Azure.
	UserCredentials *genruntime.SecretDestination `json:"userCredentials,omitempty"`
}

// AROClusterStatus defines the observed state of AROCluster.
type AROClusterStatus struct {
	// Ready is when the AROControlPlane has a API server URL.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// FailureDomains specifies a list fo available availability zones that can be used
	// +optional
	FailureDomains clusterv1.FailureDomains `json:"failureDomains,omitempty"`

	// Conditions define the current service state of the AROCluster.
	Conditions clusterv1.Conditions `json:"conditions,omitempty"`

	// TODO: mveber - added

	// LongRunningOperationStates saves the state for ARO long-running operations so they can be continued on the
	// next reconciliation loop.
	// +optional
	LongRunningOperationStates infrav1.Futures `json:"longRunningOperationStates,omitempty"`

	CurrentKubernetesVersion *string `json:"currentKubernetesVersion,omitempty"`

	// PrivateFQDN: The FQDN of private cluster.
	PrivateFQDN *string `json:"privateFQDN,omitempty"`

	// Fqdn: The FQDN of the master pool.
	Fqdn *string `json:"fqdn,omitempty"`

	// AadProfile: The Azure Active Directory configuration.
	AadProfile *AROAADProfile `json:"aadProfile,omitempty"`

	// DisableLocalAccounts: If set to true, getting static credentials will be disabled for this cluster. This must only be
	// used on Managed Clusters that are AAD enabled. For more details see [disable local
	// accounts](https://docs.microsoft.com/azure/aks/managed-aad#disable-local-accounts-preview).
	DisableLocalAccounts *bool `json:"disableLocalAccounts,omitempty"`
}

// AROAADProfile
// TODO: mveber - added
type AROAADProfile struct {
	// AdminGroupObjectIDs: The list of AAD group object IDs that will have admin role of the cluster.
	AdminGroupObjectIDs []string `json:"adminGroupObjectIDs,omitempty"`

	// ClientAppID: (DEPRECATED) The client AAD application ID. Learn more at https://aka.ms/aks/aad-legacy.
	ClientAppID *string `json:"clientAppID,omitempty"`

	// EnableAzureRBAC: Whether to enable Azure RBAC for Kubernetes authorization.
	EnableAzureRBAC *bool `json:"enableAzureRBAC,omitempty"`

	// Managed: Whether to enable managed AAD.
	Managed *bool `json:"managed,omitempty"`

	// ServerAppID: (DEPRECATED) The server AAD application ID. Learn more at https://aka.ms/aks/aad-legacy.
	ServerAppID *string `json:"serverAppID,omitempty"`

	// ServerAppSecret: (DEPRECATED) The server AAD application secret. Learn more at https://aka.ms/aks/aad-legacy.
	ServerAppSecret *string `json:"serverAppSecret,omitempty"`

	// TenantID: The AAD tenant ID to use for authentication. If not specified, will use the tenant of the deployment
	// subscription.
	TenantID *string `json:"tenantID,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=aroclusters,scope=Namespaced,categories=cluster-api,shortName=aroc
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Cluster",type="string",JSONPath=".metadata.labels.cluster\\.x-k8s\\.io/cluster-name",description="Cluster to which this AroManagedControl belongs"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.ready",description="Control plane infrastructure is ready for worker nodes"
// +kubebuilder:printcolumn:name="Endpoint",type="string",JSONPath=".spec.controlPlaneEndpoint.host",description="API Endpoint",priority=1
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=aroclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=aroclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=aroclusters/finalizers,verbs=update

// AROCluster is the Schema for the AROClusters API.
type AROCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AROClusterSpec   `json:"spec,omitempty"`
	Status AROClusterStatus `json:"status,omitempty"`
}

// TODO: mveber - added
func (ac *AROCluster) GetConditions() clusterv1.Conditions {
	return ac.Status.Conditions
}

// TODO: mveber - added
func (ac *AROCluster) SetConditions(conditions clusterv1.Conditions) {
	ac.Status.Conditions = conditions
}

// TODO: mveber - added
func (ac *AROCluster) GetFutures() infrav1.Futures {
	return ac.Status.LongRunningOperationStates
}

// TODO: mveber - added
func (ac *AROCluster) SetFutures(futures infrav1.Futures) {
	ac.Status.LongRunningOperationStates = futures
}

const (
	// AROClusterKind is the kind for AROCluster.
	AROClusterKind = "AROCluster"

	// AROClusterFinalizer is the finalizer added to AROControlPlanes.
	AROClusterFinalizer = "arocluster/finalizer"
)

// +kubebuilder:object:root=true

// AROClusterList contains a list of AROCluster.
type AROClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AROCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AROCluster{}, &AROClusterList{})
}
