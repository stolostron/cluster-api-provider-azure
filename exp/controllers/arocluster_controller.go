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

package controllers

import (
	"context"
	"errors"
	"fmt"
	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
	"sigs.k8s.io/cluster-api-provider-azure/controllers"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/patch"
	"sigs.k8s.io/cluster-api/util/predicates"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	cplanev2exp "sigs.k8s.io/cluster-api-provider-azure/exp/api/controlplane/v1beta2"
	infrav2exp "sigs.k8s.io/cluster-api-provider-azure/exp/api/v1beta2"
	"sigs.k8s.io/cluster-api-provider-azure/util/tele"
)

var errInvalidControlPlaneKind = errors.New("AROCluster cannot be used without AROControlPlane")

// AROClusterReconciler reconciles a AROCluster object.
type AROClusterReconciler struct {
	client.Client
	WatchFilterValue string
}

// SetupWithManager sets up the controller with the Manager.
func (r *AROClusterReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, options controller.Options) error {
	ctx, log, done := tele.StartSpanWithLogger(ctx,
		"controllers.AROClusterReconciler.SetupWithManager",
		tele.KVP("controller", infrav2exp.AROClusterKind),
	)
	defer done()

	_, err := ctrl.NewControllerManagedBy(mgr).
		WithOptions(options).
		For(&infrav2exp.AROCluster{}).
		WithEventFilter(predicates.ResourceHasFilterLabel(mgr.GetScheme(), log, r.WatchFilterValue)).
		WithEventFilter(predicates.ResourceIsNotExternallyManaged(mgr.GetScheme(), log)).
		// Watch clusters for pause/unpause notifications
		Watches(
			&clusterv1.Cluster{},
			handler.EnqueueRequestsFromMapFunc(
				util.ClusterToInfrastructureMapFunc(ctx, infrav2exp.GroupVersion.WithKind(infrav2exp.AROClusterKind), mgr.GetClient(), &infrav2exp.AROCluster{}),
			),
			builder.WithPredicates(
				predicates.ResourceHasFilterLabel(mgr.GetScheme(), log, r.WatchFilterValue),
				controllers.ClusterUpdatePauseChange(log),
			),
		).
		Watches(
			&cplanev2exp.AROControlPlane{},
			handler.EnqueueRequestsFromMapFunc(aroControlPlaneToAroClusterMap(r.Client, log)),
			builder.WithPredicates(
				predicates.ResourceHasFilterLabel(mgr.GetScheme(), log, r.WatchFilterValue),
				predicate.Funcs{
					CreateFunc: func(ev event.CreateEvent) bool {
						controlPlane := ev.Object.(*cplanev2exp.AROControlPlane)
						return !controlPlane.Status.ControlPlaneEndpoint.IsZero()
					},
					UpdateFunc: func(ev event.UpdateEvent) bool {
						oldControlPlane := ev.ObjectOld.(*cplanev2exp.AROControlPlane)
						newControlPlane := ev.ObjectNew.(*cplanev2exp.AROControlPlane)
						return oldControlPlane.Status.ControlPlaneEndpoint !=
							newControlPlane.Status.ControlPlaneEndpoint
					},
				},
			),
		).
		Build(r)
	if err != nil {
		return err
	}

	return nil
}

func aroControlPlaneToAroClusterMap(c client.Client, log logr.Logger) handler.MapFunc {
	return func(ctx context.Context, o client.Object) []reconcile.Request {
		aroControlPlane, ok := o.(*cplanev2exp.AROControlPlane)
		if !ok {
			log.Error(fmt.Errorf("expected a AROControlPlane, got %T instead", o), "failed to map AROControlPlane")
			return nil
		}

		log := log.WithValues("objectMapper", "arocpToaroc", "AROcontrolplane", klog.KRef(aroControlPlane.Namespace, aroControlPlane.Name))

		if !aroControlPlane.ObjectMeta.DeletionTimestamp.IsZero() {
			log.Info("AROControlPlane has a deletion timestamp, skipping mapping")
			return nil
		}

		if aroControlPlane.Status.ControlPlaneEndpoint.IsZero() { // TODO: mveber - rosa has Spec.ControlPlaneEndpoint
			log.V(4).Info("AROControlPlane has no control plane endpoint, skipping mapping")
			return nil
		}

		cluster, err := util.GetOwnerCluster(ctx, c, aroControlPlane.ObjectMeta)
		if err != nil {
			log.Error(err, "failed to get owning cluster")
			return nil
		}
		if cluster == nil {
			log.Info("no owning cluster, skipping mapping")
			return nil
		}

		aroClusterRef := cluster.Spec.InfrastructureRef
		if aroClusterRef == nil ||
			!matchesAROAPIGroup(aroClusterRef.APIVersion) ||
			aroClusterRef.Kind != infrav2exp.AROClusterKind {
			return nil
		}

		return []reconcile.Request{
			{
				NamespacedName: client.ObjectKey{
					Namespace: aroClusterRef.Namespace,
					Name:      aroClusterRef.Name,
				},
			},
		}
	}
}

func matchesAROAPIGroup(apiVersion string) bool {
	gv, _ := schema.ParseGroupVersion(apiVersion)
	return gv.Group == infrav2exp.GroupVersion.Group
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=aroclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=aroclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=aroclusters/finalizers,verbs=update

// Reconcile reconciles an AROCluster.
func (r *AROClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, resultErr error) {
	ctx, log, done := tele.StartSpanWithLogger(ctx,
		"controllers.AROClusterReconciler.Reconcile",
		tele.KVP("namespace", req.Namespace),
		tele.KVP("name", req.Name),
		tele.KVP("kind", infrav2exp.AROClusterKind),
	)
	defer done()

	log = log.WithValues("namespace", req.Namespace, "AROCluster", req.Name)

	// Fetch the AROCluster instance
	aroCluster := &infrav2exp.AROCluster{}
	err := r.Get(ctx, req.NamespacedName, aroCluster)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patchHelper, err := patch.NewHelper(aroCluster, r.Client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create patch helper: %w", err)
	}
	defer func() {
		err := patchHelper.Patch(ctx, aroCluster)
		if err != nil && resultErr == nil {
			resultErr = err
			result = ctrl.Result{}
		}
	}()

	aroCluster.Status.Ready = false

	// Fetch the Cluster.
	cluster, err := util.GetOwnerCluster(ctx, r.Client, aroCluster.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}

	if cluster != nil && cluster.Spec.Paused ||
		annotations.HasPaused(aroCluster) {
		return r.reconcilePaused(ctx, aroCluster)
	}

	if !aroCluster.GetDeletionTimestamp().IsZero() {
		return r.reconcileDelete(ctx, aroCluster)
	}

	return r.reconcileNormal(ctx, aroCluster, cluster)
}

func matchesAROControlPlaneAPIGroup(apiVersion string) bool {
	gv, _ := schema.ParseGroupVersion(apiVersion)
	return gv.Group == cplanev2exp.GroupVersion.Group
}

func (r *AROClusterReconciler) reconcileNormal(ctx context.Context, aroCluster *infrav2exp.AROCluster, cluster *clusterv1.Cluster) (ctrl.Result, error) {
	ctx, log, done := tele.StartSpanWithLogger(ctx,
		"controllers.AROClusterReconciler.reconcileNormal",
	)
	defer done()
	log.V(4).Info("reconciling normally")

	if cluster == nil {
		log.V(4).Info("Cluster Controller has not yet set OwnerRef")
		return ctrl.Result{}, nil
	}
	if cluster.Spec.ControlPlaneRef == nil ||
		!matchesAROControlPlaneAPIGroup(cluster.Spec.ControlPlaneRef.APIVersion) ||
		cluster.Spec.ControlPlaneRef.Kind != cplanev2exp.AROControlPlaneKind {
		return ctrl.Result{}, reconcile.TerminalError(errInvalidControlPlaneKind)
	}

	needsPatch := controllerutil.AddFinalizer(aroCluster, infrav2exp.AROClusterFinalizer)
	needsPatch = controllers.AddBlockMoveAnnotation(aroCluster) || needsPatch
	if needsPatch {
		return ctrl.Result{Requeue: true}, nil
	}

	aroControlPlane := &cplanev2exp.AROControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: cluster.Spec.ControlPlaneRef.Namespace,
			Name:      cluster.Spec.ControlPlaneRef.Name,
		},
	}
	err := r.Get(ctx, client.ObjectKeyFromObject(aroControlPlane), aroControlPlane)
	if client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get AROControlPlane %s/%s: %w", aroControlPlane.Namespace, aroControlPlane.Name, err)
	}

	// Set the values from the managed control plane
	aroCluster.Spec.ControlPlaneEndpoint = aroControlPlane.Status.ControlPlaneEndpoint // TODO: mveber - rosa has aroControlPlane.Spec :(

	// TODO: mveber - rosa sets true unconditionaly
	aroCluster.Status.Ready = !aroCluster.Spec.ControlPlaneEndpoint.IsZero()

	log.Info("Successfully reconciled AROCluster")

	return ctrl.Result{}, nil
}

func (r *AROClusterReconciler) reconcilePaused(ctx context.Context, aroCluster *infrav2exp.AROCluster) (ctrl.Result, error) {
	ctx, log, done := tele.StartSpanWithLogger(ctx, "controllers.AROClusterReconciler.reconcilePaused")
	defer done()
	log.V(4).Info("reconciling pause")

	controllers.RemoveBlockMoveAnnotation(aroCluster)

	return ctrl.Result{}, nil
}

func (r *AROClusterReconciler) reconcileDelete(ctx context.Context, aroCluster *infrav2exp.AROCluster) (ctrl.Result, error) {
	ctx, log, done := tele.StartSpanWithLogger(ctx,
		"controllers.AROClusterReconciler.reconcileDelete",
	)
	defer done()
	log.V(4).Info("reconciling delete")

	controllerutil.RemoveFinalizer(aroCluster, infrav2exp.AROClusterFinalizer)
	return ctrl.Result{}, nil
}
