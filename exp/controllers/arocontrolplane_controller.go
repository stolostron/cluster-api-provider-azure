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
	"errors"
	"fmt"
	"sigs.k8s.io/cluster-api-provider-azure/controllers"
	"sigs.k8s.io/cluster-api-provider-azure/pkg/mutators"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-service-operator/v2/pkg/genruntime"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/controllers/external"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"
	"sigs.k8s.io/cluster-api/util/secret"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	control2exp "sigs.k8s.io/cluster-api-provider-azure/exp/api/controlplane/v1beta2"
	infrav2exp "sigs.k8s.io/cluster-api-provider-azure/exp/api/v1beta2"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
)

var errInvalidClusterKind = errors.New("AROControlPlane cannot be used without AROCluster")
var ErrNoAROClusterDefined = fmt.Errorf("no %s AROCluster defined in AROControlPlane spec.resources", infrav2exp.GroupVersion.Group)

type aroResourceReconciler interface {
	// Reconcile reconciles resources defined by this object and updates this object's status to reflect the
	// state of the specified resources.
	Reconcile(context.Context) error

	// Pause stops ARO from continuously reconciling the specified resources.
	Pause(context.Context) error

	// Delete begins deleting the specified resources and updates the object's status to reflect the state of
	// the specified resources.
	Delete(context.Context) error
}

// AROControlPlaneReconciler reconciles a AROControlPlane object.
type AROControlPlaneReconciler struct {
	client.Client
	WatchFilterValue string
	CredentialCache  AROCredentialCache

	newResourceReconciler func(*control2exp.AROControlPlane, []*unstructured.Unstructured) aroResourceReconciler
}

// SetupWithManager sets up the controller with the Manager.
func (r *AROControlPlaneReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, options controller.Options) error {
	_, log, done := tele.StartSpanWithLogger(ctx,
		"controllers.AROControlPlaneReconciler.SetupWithManager",
		tele.KVP("controller", control2exp.AROControlPlaneKind),
	)
	defer done()

	c, err := ctrl.NewControllerManagedBy(mgr).
		WithOptions(options).
		For(&control2exp.AROControlPlane{}).
		WithEventFilter(predicates.ResourceHasFilterLabel(mgr.GetScheme(), log, r.WatchFilterValue)).
		Watches(&clusterv1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(clusterToAROControlPlane),
			builder.WithPredicates(
				predicates.ResourceHasFilterLabel(mgr.GetScheme(), log, r.WatchFilterValue),
				controllers.ClusterPauseChangeAndInfrastructureReady(mgr.GetScheme(), log),
			),
		).
		// User errors that CAPZ passes through agentPoolProfiles on create must be fixed in the
		// AROMachinePool, so trigger a reconciliation to consume those fixes.
		Watches(
			&infrav2exp.AROMachinePool{},
			handler.EnqueueRequestsFromMapFunc(r.aroMachinePoolToAROControlPlane),
		).
		Owns(&corev1.Secret{}).
		Build(r)
	if err != nil {
		return err
	}

	externalTracker := &external.ObjectTracker{
		Cache:           mgr.GetCache(),
		Controller:      c,
		Scheme:          mgr.GetScheme(),
		PredicateLogger: &log,
	}

	r.newResourceReconciler = func(aroCluster *control2exp.AROControlPlane, resources []*unstructured.Unstructured) aroResourceReconciler {
		return &ResourceReconciler{
			Client:    r.Client,
			resources: resources,
			owner:     aroCluster,
			watcher:   externalTracker,
		}
	}

	return nil
}

func clusterToAROControlPlane(_ context.Context, o client.Object) []ctrl.Request {
	controlPlaneRef := o.(*clusterv1.Cluster).Spec.ControlPlaneRef
	if controlPlaneRef != nil &&
		controlPlaneRef.APIVersion == infrav2exp.GroupVersion.Identifier() &&
		controlPlaneRef.Kind == control2exp.AROControlPlaneKind {
		return []ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: controlPlaneRef.Namespace, Name: controlPlaneRef.Name}}}
	}
	return nil
}

func (r *AROControlPlaneReconciler) aroMachinePoolToAROControlPlane(ctx context.Context, o client.Object) []ctrl.Request {
	aroMachinePool := o.(*infrav2exp.AROMachinePool)
	clusterName := aroMachinePool.Labels[clusterv1.ClusterNameLabel]
	if clusterName == "" {
		return nil
	}
	cluster, err := util.GetClusterByName(ctx, r.Client, aroMachinePool.Namespace, clusterName)
	if client.IgnoreNotFound(err) != nil || cluster == nil {
		return nil
	}
	return clusterToAROControlPlane(ctx, cluster)
}

//+kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=arocontrolplanes,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=arocontrolplanes/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=controlplane.cluster.x-k8s.io,resources=arocontrolplanes/finalizers,verbs=update

// Reconcile reconciles an AROControlPlane.
func (r *AROControlPlaneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, resultErr error) {
	ctx, log, done := tele.StartSpanWithLogger(ctx,
		"controllers.AROControlPlaneReconciler.Reconcile",
		tele.KVP("namespace", req.Namespace),
		tele.KVP("name", req.Name),
		tele.KVP("kind", control2exp.AROControlPlaneKind),
	)
	defer done()

	log = log.WithValues("namespace", req.Namespace, "azureControlPlane", req.Name)

	aroControlPlane := &control2exp.AROControlPlane{}
	err := r.Get(ctx, req.NamespacedName, aroControlPlane)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper, err := patch.NewHelper(aroControlPlane, r.Client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create patch helper: %w", err)
	}
	defer func() {
		err := patchHelper.Patch(ctx, aroControlPlane)
		if err != nil && resultErr == nil {
			resultErr = err
			result = ctrl.Result{}
		}
	}()

	aroControlPlane.Status.Ready = false
	aroControlPlane.Status.Initialized = false

	cluster, err := util.GetOwnerCluster(ctx, r.Client, aroControlPlane.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}

	if cluster != nil && cluster.Spec.Paused ||
		annotations.HasPaused(aroControlPlane) {
		return r.reconcilePaused(ctx, aroControlPlane)
	}

	if !aroControlPlane.GetDeletionTimestamp().IsZero() {
		return r.reconcileDelete(ctx, aroControlPlane)
	}

	return r.reconcileNormal(ctx, aroControlPlane, cluster)
}

func (r *AROControlPlaneReconciler) reconcileNormal(ctx context.Context, aroControlPlane *control2exp.AROControlPlane, cluster *clusterv1.Cluster) (ctrl.Result, error) {
	ctx, log, done := tele.StartSpanWithLogger(ctx,
		"controllers.AROControlPlaneReconciler.reconcileNormal",
	)
	defer done()
	log.V(4).Info("reconciling normally")

	if cluster == nil {
		log.V(4).Info("Cluster Controller has not yet set OwnerRef")
		return ctrl.Result{}, nil
	}
	if cluster.Spec.InfrastructureRef == nil ||
		cluster.Spec.InfrastructureRef.APIVersion != infrav2exp.GroupVersion.Identifier() ||
		cluster.Spec.InfrastructureRef.Kind != infrav2exp.AROClusterKind {
		return ctrl.Result{}, reconcile.TerminalError(errInvalidClusterKind)
	}

	needsPatch := controllerutil.AddFinalizer(aroControlPlane, control2exp.AROControlPlaneFinalizer)
	needsPatch = controllers.AddBlockMoveAnnotation(aroControlPlane) || needsPatch
	if needsPatch {
		return ctrl.Result{Requeue: true}, nil
	}

	if aroControlPlane.Spec.AroClusterName == "" {
		return ctrl.Result{}, reconcile.TerminalError(ErrNoAROClusterDefined)
	}

	/*
		resourceReconciler := r.newResourceReconciler(aroControlPlane, resources)
		err = resourceReconciler.Reconcile(ctx)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile resources: %w", err)
		}
		for _, status := range aroControlPlane.Status.Resources {
			if !status.Ready {
				return ctrl.Result{}, nil
			}
		}
	*/

	aroCluster := &infrav2exp.AROCluster{}
	err := r.Get(ctx, client.ObjectKey{Namespace: aroControlPlane.Namespace, Name: aroControlPlane.Spec.AroClusterName}, aroCluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("error getting AroCluster: %w", err)
	}

	aroControlPlane.Status.ControlPlaneEndpoint = getControlPlaneEndpoint(aroCluster)
	if aroCluster.Status.CurrentKubernetesVersion != nil {
		aroControlPlane.Status.Version = "v" + *aroCluster.Status.CurrentKubernetesVersion
	}

	/*
		tokenExpiresIn, err := r.reconcileKubeconfig(ctx, aroControlPlane, cluster, aroCluster)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to reconcile kubeconfig: %w", err)
		}
		if tokenExpiresIn != nil && *tokenExpiresIn <= 0 { // the token has already expired
			return ctrl.Result{Requeue: true}, nil
		}
		// ensure we refresh the token when it expires
		result := ctrl.Result{RequeueAfter: ptr.Deref(tokenExpiresIn, 0)}
	*/
	result := ctrl.Result{RequeueAfter: time.Second * 30}

	aroControlPlane.Status.Ready = !aroControlPlane.Status.ControlPlaneEndpoint.IsZero()
	// The AKS API doesn't allow us to distinguish between CAPI's definitions of "initialized" and "ready" so
	// we treat them equivalently.
	aroControlPlane.Status.Initialized = aroControlPlane.Status.Ready

	return result, nil
}

func (r *AROControlPlaneReconciler) reconcileKubeconfig(ctx context.Context, aroControlPlane *control2exp.AROControlPlane, cluster *clusterv1.Cluster, aroCluster *infrav2exp.AROCluster) (*time.Duration, error) {
	ctx, log, done := tele.StartSpanWithLogger(ctx,
		"controllers.AROControlPlaneReconciler.reconcileKubeconfig",
	)
	defer done()

	var secretRef *genruntime.SecretDestination
	if aroCluster.Spec.Secrets != nil {
		secretRef = aroCluster.Spec.Secrets.UserCredentials
		if aroCluster.Spec.Secrets.AdminCredentials != nil {
			secretRef = aroCluster.Spec.Secrets.AdminCredentials
		}
	}
	if secretRef == nil {
		return nil, reconcile.TerminalError(fmt.Errorf("AROCluster must define at least one of spec.operatorSpec.secrets.{userCredentials,adminCredentials}"))
	}
	aroKubeconfig := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: secretRef.Name}, aroKubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch secret created by ARO: %w", err)
	}

	kubeconfigData := aroKubeconfig.Data[secretRef.Key]
	var tokenExpiresIn *time.Duration

	if aroCluster.Status.AadProfile != nil &&
		ptr.Deref(aroCluster.Status.AadProfile.Managed, false) &&
		ptr.Deref(aroCluster.Status.DisableLocalAccounts, false) {
		if secretRef.Name == secret.Name(cluster.Name, secret.Kubeconfig) {
			return nil, fmt.Errorf("ARO-generated kubeconfig Secret name cannot be %q when local accounts are disabled on the AROCluster, CAPZ must be able to create and manage its own Secret with that name in order to augment the kubeconfig without conflicting with ARO", secretRef.Name)
		}

		// Admin credentials cannot be retrieved when local accounts are disabled. Fetch a Bearer token like
		// `kubelogin` would and set it in the kubeconfig to remove the need for that binary in CAPI controllers.
		cred, err := r.CredentialCache.authTokenForAROResource(ctx, aroCluster)
		if err != nil {
			return nil, err
		}
		// magic string for AKS's managed Entra server ID: https://learn.microsoft.com/azure/aks/kubelogin-authentication#how-to-use-kubelogin-with-aks
		token, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{"6dae42f8-4368-4678-94ff-3960e28e3630/.default"}})
		if err != nil {
			return nil, err
		}
		tokenExpiresIn = ptr.To(time.Until(token.ExpiresOn))
		log.V(4).Info("retrieved access token", "expiresOn", token.ExpiresOn, "expiresIn", tokenExpiresIn)

		kubeconfig, err := clientcmd.Load(kubeconfigData)
		if err != nil {
			return nil, err
		}
		for _, a := range kubeconfig.AuthInfos {
			a.Exec = nil
			a.Token = token.Token
		}
		kubeconfigData, err = clientcmd.Write(*kubeconfig)
		if err != nil {
			return nil, err
		}
	}

	expectedSecret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.Identifier(),
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      secret.Name(cluster.Name, secret.Kubeconfig),
			Namespace: cluster.Namespace,
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(aroControlPlane, infrav2exp.GroupVersion.WithKind(control2exp.AROControlPlaneKind)),
			},
			Labels: map[string]string{clusterv1.ClusterNameLabel: cluster.Name},
		},
		Data: map[string][]byte{
			secret.KubeconfigDataName: kubeconfigData,
		},
	}

	err = r.Patch(ctx, expectedSecret, client.Apply, client.FieldOwner("capz-manager"), client.ForceOwnership)
	if err != nil {
		return nil, err
	}
	return tokenExpiresIn, nil
}

func (r *AROControlPlaneReconciler) reconcilePaused(ctx context.Context, aroControlPlane *control2exp.AROControlPlane) (ctrl.Result, error) {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "controllers.AROControlPlaneReconciler.reconcilePaused")
	defer done()
	log.V(4).Info("reconciling pause")

	resources, err := mutators.ToUnstructured(ctx, aroControlPlane.Spec.Resources)
	if err != nil {
		return ctrl.Result{}, err
	}
	resourceReconciler := r.newResourceReconciler(aroControlPlane, resources)
	err = resourceReconciler.Pause(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to pause resources: %w", err)
	}

	controllers.RemoveBlockMoveAnnotation(aroControlPlane)

	return ctrl.Result{}, nil
}

func (r *AROControlPlaneReconciler) reconcileDelete(ctx context.Context, aroControlPlane *control2exp.AROControlPlane) (ctrl.Result, error) {
	ctx, log, done := tele.StartSpanWithLogger(ctx,
		"controllers.AROControlPlaneReconciler.reconcileDelete",
	)
	defer done()
	log.V(4).Info("reconciling delete")

	resources, err := mutators.ToUnstructured(ctx, aroControlPlane.Spec.Resources)
	if err != nil {
		return ctrl.Result{}, err
	}
	resourceReconciler := r.newResourceReconciler(aroControlPlane, resources)
	err = resourceReconciler.Delete(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile resources: %w", err)
	}
	if len(aroControlPlane.Status.Resources) > 0 {
		return ctrl.Result{}, nil
	}

	controllerutil.RemoveFinalizer(aroControlPlane, control2exp.AROControlPlaneFinalizer)
	return ctrl.Result{}, nil
}

func getControlPlaneEndpoint(aroCluster *infrav2exp.AROCluster) clusterv1.APIEndpoint {
	if aroCluster.Status.PrivateFQDN != nil {
		return clusterv1.APIEndpoint{
			Host: *aroCluster.Status.PrivateFQDN,
			Port: 443,
		}
	}
	if aroCluster.Status.Fqdn != nil {
		return clusterv1.APIEndpoint{
			Host: *aroCluster.Status.Fqdn,
			Port: 443,
		}
	}
	return clusterv1.APIEndpoint{}
}
