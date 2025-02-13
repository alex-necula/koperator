// Copyright © 2022 Cisco Systems, Inc. and/or its affiliates
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controllers

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/banzaicloud/koperator/api/v1alpha1"
	banzaiv1alpha1 "github.com/banzaicloud/koperator/api/v1alpha1"
)

// CruiseControlOperationTTLReconciler reconciles CruiseControlOperation custom resources
type CruiseControlOperationTTLReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=kafka.banzaicloud.io,resources=cruisecontroloperations,verbs=get;list;watch;create;update;patch;delete;deletecollection
// +kubebuilder:rbac:groups=kafka.banzaicloud.io,resources=cruisecontroloperations/status,verbs=get

//nolint:gocyclo
func (r *CruiseControlOperationTTLReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logr.FromContextOrDiscard(ctx)
	log.V(1).Info("reconciling CruiseControlOperation custom resources")

	ccOperation := &banzaiv1alpha1.CruiseControlOperation{}
	if err := r.Get(ctx, client.ObjectKey{Name: req.Name, Namespace: req.Namespace}, ccOperation); err != nil {
		if apierrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			return reconciled()
		}
		// Error reading the object - requeue the request.
		return requeueWithError(log, err.Error(), err)
	}

	if ccOperation.GetTTLSecondsAfterFinished() == nil || ccOperation.CurrentTaskFinished() == nil {
		return reconciled()
	}

	operationTTL := time.Duration(*ccOperation.GetTTLSecondsAfterFinished()) * time.Second
	finishedAt := ccOperation.CurrentTaskFinished()
	cleanupTime := finishedAt.Time.Add(operationTTL)

	if IsExpired(operationTTL, finishedAt.Time) {
		log.V(1).Info("cleaning up finished CruiseControlOperation", "finished", finishedAt.Time, "clean-up time", cleanupTime)
		return r.delete(ctx, ccOperation)
	}
	// +1 sec is needed to be sure, because double to int conversion round down
	reqSec := int(time.Until(cleanupTime).Seconds() + 1)
	log.V(1).Info("requeue later to clean up CruiseControlOperation", "clean-up time", cleanupTime)
	return requeueAfter(reqSec)
}

// SetupCruiseControlWithManager registers cruise control operation controller to the manager
func SetupCruiseControlOperationTTLWithManager(mgr ctrl.Manager) *ctrl.Builder {
	cruiseControlOperationTTLPredicate := predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			obj := e.Object.(*banzaiv1alpha1.CruiseControlOperation)
			if obj.IsFinished() && obj.GetTTLSecondsAfterFinished() != nil && obj.GetDeletionTimestamp().IsZero() {
				return true
			}
			return false
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			newObj := e.ObjectNew.(*banzaiv1alpha1.CruiseControlOperation)
			if newObj.IsFinished() && newObj.GetTTLSecondsAfterFinished() != nil && newObj.GetDeletionTimestamp().IsZero() {
				return true
			}
			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
	}

	builder := ctrl.NewControllerManagedBy(mgr).
		For(&banzaiv1alpha1.CruiseControlOperation{}).
		WithEventFilter(SkipClusterRegistryOwnedResourcePredicate{}).
		WithEventFilter(cruiseControlOperationTTLPredicate).
		Named("CruiseControlOperationTTL")

	return builder
}

func IsExpired(ttl time.Duration, finishedAt time.Time) bool {
	return time.Since(finishedAt) > ttl
}

func (r *CruiseControlOperationTTLReconciler) delete(ctx context.Context, ccOperation *v1alpha1.CruiseControlOperation) (reconcile.Result, error) {
	log := logr.FromContextOrDiscard(ctx)
	err := r.Delete(ctx, ccOperation)
	if err != nil && !apierrors.IsNotFound(err) {
		return requeueWithError(log, "error is occurred when deleting finished CruiseControlOperation ", err)
	}

	return reconcile.Result{}, nil
}
