// Copyright 2019 The ctrlarm Authors
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

package databaseaccount

import (
	"context"
	"time"

	cosmosdbv1alpha1 "github.com/juan-lee/ctrlarm/pkg/apis/cosmosdb/v1alpha1"
	"github.com/juan-lee/ctrlarm/pkg/services/azure/cosmosdb"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const cosmosDBFinalizer = "cosmosdb.resources.azure.com"

var log = logf.Log.WithName("controller")

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new DatabaseAccount Controller and adds it to the Manager with default RBAC. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcileDatabaseAccount{Client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("databaseaccount-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to DatabaseAccount
	err = c.Watch(&source.Kind{Type: &cosmosdbv1alpha1.DatabaseAccount{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcileDatabaseAccount{}

// ReconcileDatabaseAccount reconciles a DatabaseAccount object
type ReconcileDatabaseAccount struct {
	client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a DatabaseAccount object and makes changes based on the state read
// and what is in the DatabaseAccount.Spec
// TODO(user): Modify this Reconcile function to implement your Controller logic.  The scaffolding writes
// a Deployment as an example
// Automatically generate RBAC rules to allow the Controller to read and write Deployments
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cosmosdb.azure.com,resources=databaseaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cosmosdb.azure.com,resources=databaseaccounts/status,verbs=get;update;patch
func (r *ReconcileDatabaseAccount) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	// Fetch the DatabaseAccount instance
	instance := &cosmosdbv1alpha1.DatabaseAccount{}
	err := r.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Object not found, return.  Created objects are automatically garbage collected.
			// For additional cleanup logic use finalizers.
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	log.Info("NewDatabaseAccountActuator", "subscriptionID", instance.Spec.SubscriptionID)
	mca, err := cosmosdb.NewDatabaseAccountActuator(context.TODO(), instance.Spec.SubscriptionID)
	if err != nil {
		return reconcile.Result{}, err
	}

	found, err := mca.Get(context.TODO(), instance.Spec.ResourceGroup, instance.Name)
	if _, ok := err.(*cosmosdb.DatabaseAccountNotFound); ok {
		if isDeletePending(instance) {
			err = r.completeFinalize(context.TODO(), instance)
			if err != nil {
				return reconcile.Result{Requeue: true}, err
			}
			return reconcile.Result{}, nil
		}

		log.Info("Create DatabaseAccount", "namespace", instance.Namespace, "name", instance.Name)
		err = mca.Create(context.TODO(), instance, nil)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 30}, nil
	} else if _, ok := err.(*cosmosdb.CosmosConnectionStringsNotFound); ok {
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 30}, nil
	} else if err != nil {
		return reconcile.Result{}, err
	}

	instance.Status.ProvisioningState = string(*found.ProvisioningState)
	log.Info("DatabaseAccount ProvisioningState", "state", instance.Status.ProvisioningState)
	if err = r.Status().Update(context.TODO(), instance); err != nil {
		return reconcile.Result{}, err
	}

	if found.IsProvisioning() {
		log.Info("Reconciling DatabaseAccount", "namespace", instance.Namespace, "name", instance.Name, "state", instance.Status.ProvisioningState)
		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 30}, nil
	}

	if !isDeletePending(instance) {
		if !hasFinalizer(instance.ObjectMeta.Finalizers) {
			err = r.addFinalizer(context.TODO(), instance)
			if err != nil {
				return reconcile.Result{Requeue: true}, err
			}
			return reconcile.Result{Requeue: true}, nil
		}
	} else {
		if hasFinalizer(instance.ObjectMeta.Finalizers) {
			log.Info("Delete DatabaseAccount", "namespace", instance.Namespace, "name", instance.Name)
			err = mca.Delete(context.TODO(), instance.Spec.ResourceGroup, instance.Name)
			if err != nil {
				return reconcile.Result{}, err
			}
			return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 30}, nil
		}
	}

	desired := found.Merge(instance, nil)
	if !found.IsEqual(desired) {
		log.Info("Update DatabaseAccount", "namespace", instance.Namespace, "name", instance.Name)
		err = mca.Update(context.TODO(), instance.Spec.ResourceGroup, instance.Name, desired)
		if err != nil {
			return reconcile.Result{}, err
		}

		return reconcile.Result{Requeue: true, RequeueAfter: time.Second * 30}, nil
	}

	return reconcile.Result{}, nil
}

func (r *ReconcileDatabaseAccount) addFinalizer(ctx context.Context, instance *cosmosdbv1alpha1.DatabaseAccount) error {
	log.Info("Adding DatabaseAccount Finalizer", "namespace", instance.Namespace, "name", instance.Name)
	instance.ObjectMeta.Finalizers = append(instance.ObjectMeta.Finalizers, cosmosDBFinalizer)
	if err := r.Update(ctx, instance); err != nil {
		return err
	}
	return nil
}

func (r *ReconcileDatabaseAccount) completeFinalize(ctx context.Context, instance *cosmosdbv1alpha1.DatabaseAccount) error {
	instance.ObjectMeta.Finalizers = removeFinializer(instance.ObjectMeta.Finalizers)
	if err := r.Update(ctx, instance); err != nil {
		return err
	}
	log.Info("DatabaseAccount Finialization Complete", "namespace", instance.Namespace, "name", instance.Name)
	return nil
}

func hasFinalizer(slice []string) bool {
	for _, item := range slice {
		if item == cosmosDBFinalizer {
			return true
		}
	}
	return false
}

func removeFinializer(slice []string) (result []string) {
	for _, item := range slice {
		if item == cosmosDBFinalizer {
			continue
		}
		result = append(result, item)
	}
	return
}

func isDeletePending(instance *cosmosdbv1alpha1.DatabaseAccount) bool {
	return !instance.ObjectMeta.DeletionTimestamp.IsZero()
}
