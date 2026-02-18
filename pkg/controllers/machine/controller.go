/*
Copyright 2026 Fabien Dupont.

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

package machine

import (
	"context"
	"fmt"
	"time"

	machinev1beta1 "github.com/openshift/api/machine/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/fabiendupont/machine-api-provider-nvidia-carbide/pkg/actuators/machine"
)

const (
	// MachineFinalizer is the finalizer for OpenShift machines
	MachineFinalizer = "machine.openshift.io/nvidia-carbide"

	// RequeueAfterSeconds is the time to wait before requeuing
	RequeueAfterSeconds = 30
)

// MachineReconciler reconciles OpenShift Machine objects
type MachineReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	Actuator      *machine.Actuator
	EventRecorder record.EventRecorder
}

// Reconcile handles Machine reconciliation
func (r *MachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the Machine instance
	machineObj := &machinev1beta1.Machine{}
	if err := r.Get(ctx, req.NamespacedName, machineObj); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("Reconciling OpenShift Machine", "machine", machineObj.GetName())

	// Handle deletion
	if !machineObj.GetDeletionTimestamp().IsZero() {
		return r.reconcileDelete(ctx, machineObj)
	}

	// Handle normal reconciliation
	return r.reconcileNormal(ctx, machineObj)
}

func (r *MachineReconciler) reconcileNormal(ctx context.Context, machineObj client.Object) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Add finalizer if it doesn't exist
	if !controllerutil.ContainsFinalizer(machineObj, MachineFinalizer) {
		controllerutil.AddFinalizer(machineObj, MachineFinalizer)
		if err := r.Update(ctx, machineObj); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// Check if instance exists
	exists, err := r.Actuator.Exists(ctx, machineObj)
	if err != nil {
		logger.Error(err, "failed to check if instance exists")
		return ctrl.Result{RequeueAfter: RequeueAfterSeconds * time.Second}, err
	}

	if !exists {
		// Create instance
		logger.Info("Creating instance")
		if err := r.Actuator.Create(ctx, machineObj); err != nil {
			logger.Error(err, "failed to create instance")
			return ctrl.Result{RequeueAfter: RequeueAfterSeconds * time.Second}, err
		}
		logger.Info("Successfully created instance")
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Update instance status
	logger.Info("Updating instance status")
	if err := r.Actuator.Update(ctx, machineObj); err != nil {
		logger.Error(err, "failed to update instance")
		return ctrl.Result{RequeueAfter: RequeueAfterSeconds * time.Second}, err
	}

	logger.Info("Successfully reconciled Machine")
	return ctrl.Result{RequeueAfter: RequeueAfterSeconds * time.Second}, nil
}

func (r *MachineReconciler) reconcileDelete(ctx context.Context, machineObj client.Object) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	logger.Info("Deleting Machine")

	// Delete instance
	if err := r.Actuator.Delete(ctx, machineObj); err != nil {
		logger.Error(err, "failed to delete instance")
		return ctrl.Result{}, err
	}

	// Remove finalizer
	controllerutil.RemoveFinalizer(machineObj, MachineFinalizer)
	if err := r.Update(ctx, machineObj); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
	}

	logger.Info("Successfully deleted Machine")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager
func (r *MachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&machinev1beta1.Machine{}).
		Complete(r)
}

// SetupMachineController creates and registers the Machine controller with the manager
func SetupMachineController(mgr ctrl.Manager, actuator *machine.Actuator) error {
	reconciler := &MachineReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		Actuator:      actuator,
		EventRecorder: mgr.GetEventRecorderFor("nvidia-carbide-machine-controller"),
	}

	return reconciler.SetupWithManager(mgr)
}
