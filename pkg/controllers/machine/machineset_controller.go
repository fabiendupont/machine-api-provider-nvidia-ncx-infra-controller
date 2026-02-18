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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// MachineSetReconciler reconciles OpenShift MachineSet objects
type MachineSetReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// Reconcile handles MachineSet reconciliation to ensure desired replicas
// Note: This is a simplified implementation. A full implementation would
// handle replica scaling, machine health checks, and more complex scenarios.
func (r *MachineSetReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the MachineSet instance
	machineSet := &machinev1beta1.MachineSet{}
	if err := r.Get(ctx, req.NamespacedName, machineSet); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("Reconciling OpenShift MachineSet", "machineSet", machineSet.GetName())

	// Get desired replicas from spec
	desiredReplicas := int32(0)
	if machineSet.Spec.Replicas != nil {
		desiredReplicas = *machineSet.Spec.Replicas
	}

	// List current machines owned by this MachineSet
	// In a full implementation, this would filter by owner reference or labels
	currentMachines := []runtime.Object{} // Placeholder
	currentReplicas := int32(len(currentMachines))

	logger.Info("MachineSet status",
		"desired", desiredReplicas,
		"current", currentReplicas)

	// Scale up if needed
	if currentReplicas < desiredReplicas {
		diff := desiredReplicas - currentReplicas
		logger.Info("Scaling up", "count", diff)

		for i := int32(0); i < diff; i++ {
			if err := r.createMachine(ctx, machineSet); err != nil {
				logger.Error(err, "failed to create machine")
				return ctrl.Result{RequeueAfter: 10 * time.Second}, err
			}
		}

		return ctrl.Result{Requeue: true}, nil
	}

	// Scale down if needed
	if currentReplicas > desiredReplicas {
		diff := currentReplicas - desiredReplicas
		logger.Info("Scaling down", "count", diff)

		for i := int32(0); i < diff; i++ {
			if i < int32(len(currentMachines)) {
				if err := r.deleteMachine(ctx, currentMachines[i]); err != nil {
					logger.Error(err, "failed to delete machine")
					return ctrl.Result{RequeueAfter: 10 * time.Second}, err
				}
			}
		}

		return ctrl.Result{Requeue: true}, nil
	}

	logger.Info("MachineSet is at desired replica count")
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *MachineSetReconciler) createMachine(ctx context.Context, machineSet *machinev1beta1.MachineSet) error {
	logger := log.FromContext(ctx)

	// In a real implementation, this would:
	// 1. Clone the machine template from MachineSet.Spec.Template
	// 2. Set owner reference to the MachineSet
	// 3. Generate a unique name
	// 4. Create the Machine resource

	logger.Info("Creating machine from MachineSet template")

	// Placeholder - actual implementation would create the Machine
	return nil
}

func (r *MachineSetReconciler) deleteMachine(ctx context.Context, machine runtime.Object) error {
	logger := log.FromContext(ctx)

	machineObj, ok := machine.(client.Object)
	if !ok {
		return fmt.Errorf("machine is not a client.Object")
	}

	logger.Info("Deleting machine", "machine", machineObj.GetName())

	// Delete the Machine resource
	if err := r.Delete(ctx, machineObj); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete machine: %w", err)
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager
func (r *MachineSetReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&machinev1beta1.MachineSet{}).
		Complete(r)
}
