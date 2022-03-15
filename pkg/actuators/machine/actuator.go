/*
Copyright oVirt Authors
SPDX-License-Identifier: Apache-2.0
*/

package machine

import (
	"context"
	"fmt"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/client-go/machine/clientset/versioned/typed/machine/v1beta1"
	ovirtconfigv1 "github.com/openshift/cluster-api-provider-ovirt/pkg/apis/ovirtprovider/v1beta1"
	ovirtC "github.com/openshift/cluster-api-provider-ovirt/pkg/clients/ovirt"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/utils"
	apierrors "github.com/openshift/machine-api-operator/pkg/controller/machine"
	ovirtclient "github.com/ovirt/go-ovirt-client"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ActuatorParams is the data structure that contains the parameters required to set up a new Actuator.
type ActuatorParams struct {
	Namespace      string
	Client         client.Client
	Scheme         *runtime.Scheme
	MachinesClient v1beta1.MachineV1beta1Interface
	EventRecorder  record.EventRecorder
}

// OvirtActuator is responsible for performing machine reconciliation on oVirt platform.
type OvirtActuator struct {
	params        ActuatorParams
	scheme        *runtime.Scheme
	client        client.Client
	eventRecorder record.EventRecorder
	ovirtClient   ovirtclient.Client
}

// NewActuator returns an Ovirt Actuator.
func NewActuator(params ActuatorParams) *OvirtActuator {
	return &OvirtActuator{
		params:        params,
		client:        params.Client,
		scheme:        params.Scheme,
		eventRecorder: params.EventRecorder,
		ovirtClient:   nil,
	}
}

// Create creates a VM on oVirt platform from the machine object and is invoked by the machine controller.
// Machine should be a valid machine object, in case a validation error occurs an InvalidMachineConfiguration
// error is returned and the Machine object will move to Failed state
func (actuator *OvirtActuator) Create(ctx context.Context, machine *machinev1.Machine) error {
	providerSpec, err := ovirtconfigv1.ProviderSpecFromRawExtension(machine.Spec.ProviderSpec.Value)
	if err != nil {
		return actuator.handleMachineError(machine, "Create", apierrors.InvalidMachineConfiguration(
			"cannot unmarshal machineProviderSpec field: %v", err))
	}

	ovirtClient, err := actuator.getClient()
	if err != nil {
		return actuator.handleMachineError(machine, "Create", apierrors.InvalidMachineConfiguration(
			"failed to create connection to oVirt API: %v", err))
	}

	if err := validateMachine(ovirtClient, providerSpec); err != nil {
		return actuator.handleMachineError(machine, "Create", apierrors.InvalidMachineConfiguration(
			"error validating machine fields: %v", err))
	}

	mScope := newMachineScope(ctx, ovirtClient, actuator.client, machine, providerSpec)
	if err := mScope.create(); err != nil {
		return actuator.handleMachineError(machine, "Create", apierrors.CreateMachine(
			"error creating Machine %v", err))
	}
	if err := mScope.reconcileMachine(ctx); err != nil {
		return actuator.handleMachineError(machine, "Create", apierrors.CreateMachine(
			"error reconciling Machine %v", err))
	}
	if err := mScope.patchMachine(ctx); err != nil {
		return actuator.handleMachineError(machine, "Create", apierrors.CreateMachine(
			"error patching Machine %v", err))
	}
	actuator.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Created", "Updated Machine %v", machine.Name)
	return nil
}

// Update attempts to sync machine state with an existing instance.
// Updating provider fields is not supported, a new machine should be created instead
func (actuator *OvirtActuator) Update(ctx context.Context, machine *machinev1.Machine) error {
	// eager update
	providerSpec, err := ovirtconfigv1.ProviderSpecFromRawExtension(machine.Spec.ProviderSpec.Value)
	if err != nil {
		return actuator.handleMachineError(machine, "Update", apierrors.InvalidMachineConfiguration(
			"cannot unmarshal machineProviderSpec field: %v", err))
	}

	ovirtClient, err := actuator.getClient()
	if err != nil {
		return actuator.handleMachineError(machine, "Update", apierrors.UpdateMachine(
			"failed to create connection to oVirt API %v", err))
	}

	mScope := newMachineScope(ctx, ovirtClient, actuator.client, machine, providerSpec)

	if err := mScope.reconcileMachine(ctx); err != nil {
		return actuator.handleMachineError(machine, "Update", apierrors.UpdateMachine(
			"error reconciling Machine %v", err))
	}

	if err := mScope.patchMachine(ctx); err != nil {
		return actuator.handleMachineError(machine, "Update", apierrors.UpdateMachine(
			"error patching Machine %v", err))
	}

	actuator.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Update", "Updated Machine %v", machine.Name)
	return nil
}

// Exists determines if the given machine currently exists.
// A machine which is not terminated is considered as existing.
func (actuator *OvirtActuator) Exists(ctx context.Context, machine *machinev1.Machine) (bool, error) {
	klog.Infof("Checking machine %v exists.\n", machine.Name)
	ovirtClient, err := actuator.getClient()
	if err != nil {
		return false, errors.Wrap(err, "failed to create connection to oVirt API")
	}
	mScope := newMachineScope(ctx, ovirtClient, actuator.client, machine, nil)

	return mScope.exists()
}

// Delete deletes the VM from the RHV environment
func (actuator *OvirtActuator) Delete(ctx context.Context, machine *machinev1.Machine) error {
	ovirtClient, err := actuator.getClient()

	if err != nil {
		return errors.Wrap(err, "failed to create connection to oVirt API")
	}

	mScope := newMachineScope(ctx, ovirtClient, actuator.client, machine, nil)
	if err := mScope.delete(); err != nil {
		return actuator.handleMachineError(machine, "Deleted", apierrors.UpdateMachine(
			"error deleting Ovirt instance %v", err))
	}
	actuator.eventRecorder.Eventf(machine, corev1.EventTypeNormal, "Deleted", "Deleted Machine %v", machine.Name)
	return nil
}

// If the OvirtActuator has a client for updating Machine objects, this will set
// the appropriate reason/message on the Machine.Status. If not, such as during
// cluster installation, it will operate as a no-op. It also returns the
// original error for convenience, so callers can do "return handleMachineError(...)".
func (actuator *OvirtActuator) handleMachineError(machine *machinev1.Machine, reason string, err *apierrors.MachineError) error {
	actuator.eventRecorder.Eventf(machine, corev1.EventTypeWarning, reason, "%v", err)

	if actuator.client != nil {
		machine.Status.ErrorReason = &err.Reason
		machine.Status.ErrorMessage = &err.Message
		if err := actuator.client.Update(context.TODO(), machine); err != nil {
			return errors.Wrap(err, "unable to update machine status")
		}
	}

	klog.Errorf("machine %s error: %v", machine.Name, err.Message)
	return err
}

// getClient returns a a client to oVirt's API endpoint
func (actuator *OvirtActuator) getClient() (ovirtclient.Client, error) {
	if actuator.ovirtClient == nil || actuator.ovirtClient.Test() != nil {
		creds, err := ovirtC.GetCredentialsSecret(actuator.client, utils.NAMESPACE, utils.OvirtCloudCredsSecretName)
		if err != nil {
			return nil, fmt.Errorf("failed getting credentials for namespace %s, %w", utils.NAMESPACE, err)
		}
		// session expired or some other error, re-login.
		actuator.ovirtClient, err = ovirtC.NewClient(creds)
		if err != nil {
			return nil, errors.Wrap(err, "failed creating ovirt connection")
		}
	}
	return actuator.ovirtClient, nil
}
