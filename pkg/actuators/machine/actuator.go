/*
Copyright oVirt Authors
SPDX-License-Identifier: Apache-2.0
*/

package machine

import (
	"context"
	"fmt"
	"time"

	osclientset "github.com/openshift/client-go/config/clientset/versioned"
	ovirtconfigv1 "github.com/openshift/cluster-api-provider-ovirt/pkg/apis/ovirtprovider/v1beta1"
	ovirtC "github.com/openshift/cluster-api-provider-ovirt/pkg/clients/ovirt"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/utils"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	apierrors "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"github.com/openshift/machine-api-operator/pkg/generated/clientset/versioned/typed/machine/v1beta1"
	ovirtsdk "github.com/ovirt/go-ovirt"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	timeoutInstanceCreate       = 5 * time.Minute
	retryIntervalInstanceStatus = 10 * time.Second
	userAgent                   = "cluster-api-provider-ovirt"
)

// ActuatorParams holds parameter information for Actuator
type ActuatorParams struct {
	Namespace      string
	Client         client.Client
	KubeClient     *kubernetes.Clientset
	Scheme         *runtime.Scheme
	MachinesClient v1beta1.MachineV1beta1Interface
	EventRecorder  record.EventRecorder
}

// OvirtActuator is responsible for performing machine reconciliation on oVirt platform.
type OvirtActuator struct {
	params          ActuatorParams
	scheme          *runtime.Scheme
	client          client.Client
	KubeClient      *kubernetes.Clientset
	machinesClient  v1beta1.MachineV1beta1Interface
	EventRecorder   record.EventRecorder
	ovirtConnection *ovirtsdk.Connection
	OSClient        osclientset.Interface
}

// NewActuator returns an Ovirt Actuator.
func NewActuator(params ActuatorParams) (*OvirtActuator, error) {
	config := ctrl.GetConfigOrDie()
	osClient := osclientset.NewForConfigOrDie(rest.AddUserAgent(config, userAgent))

	return &OvirtActuator{
		params:          params,
		client:          params.Client,
		machinesClient:  params.MachinesClient,
		scheme:          params.Scheme,
		KubeClient:      params.KubeClient,
		EventRecorder:   params.EventRecorder,
		ovirtConnection: nil,
		OSClient:        osClient,
	}, nil
}

// Create creates a VM on oVirt platform from the machine object and is invoked by the machine controller.
// Machine should be a valid machine object, in case a validation error occurs an InvalidMachineConfiguration
// error is returned and the Machine object will move to Failed state
func (actuator *OvirtActuator) Create(ctx context.Context, machine *machinev1.Machine) error {
	providerSpec, err := ovirtconfigv1.ProviderSpecFromRawExtension(machine.Spec.ProviderSpec.Value)
	if err != nil {
		return actuator.handleMachineError(machine, "Create", apierrors.InvalidMachineConfiguration(
			"Cannot unmarshal machineProviderSpec field: %v", err))
	}

	connection, err := actuator.getConnection()
	if err != nil {
		return actuator.handleMachineError(machine, "Create", apierrors.InvalidMachineConfiguration(
			"failed to create connection to oVirt API: %v", err))
	}

	ovirtClient := ovirtC.NewOvirtClient(connection)
	mScope := newMachineScope(ctx, ovirtClient, actuator.client, actuator.machinesClient, machine, providerSpec)

	if vErr := validateMachine(ovirtClient, providerSpec); vErr != nil {
		return vErr
	}
	if err := mScope.create(); err != nil {
		return actuator.handleMachineError(machine, "Create", apierrors.CreateMachine(
			"Error creating Machine %v", err))
	}

	if err := mScope.patchMachine(ctx, conditionSuccess()); err != nil {
		return actuator.handleMachineError(machine, "Create", apierrors.CreateMachine(
			"Error patching Machine %v", err))
		return err
	}
	actuator.EventRecorder.Eventf(machine, corev1.EventTypeNormal, "Created", "Updated Machine %v", machine.Name)
	return nil
}

// Exists determines if the given machine currently exists.
// A machine which is not terminated is considered as existing.
func (actuator *OvirtActuator) Exists(ctx context.Context, machine *machinev1.Machine) (bool, error) {
	klog.Infof("Checking machine %v exists.\n", machine.Name)
	connection, err := actuator.getConnection()
	if err != nil {
		return false, fmt.Errorf("failed to create connection to oVirt API")
	}
	ovirtClient := ovirtC.NewOvirtClient(connection)
	if err != nil {
		return false, err
	}
	mScope := newMachineScope(ctx, ovirtClient, actuator.client, actuator.machinesClient, machine, nil)
	return mScope.exists()
}

// Update attempts to sync machine state with an existing instance.
// Updating provider fields is not supported, a new machine should be created instead
func (actuator *OvirtActuator) Update(ctx context.Context, machine *machinev1.Machine) error {
	// eager update
	providerSpec, err := ovirtconfigv1.ProviderSpecFromRawExtension(machine.Spec.ProviderSpec.Value)
	if err != nil {
		return actuator.handleMachineError(machine, "Update", apierrors.InvalidMachineConfiguration(
			"Cannot unmarshal machineProviderSpec field: %v", err))
	}

	connection, err := actuator.getConnection()
	if err != nil {
		return actuator.handleMachineError(machine, "Update", apierrors.UpdateMachine(
			"failed to create connection to oVirt API %v", err))
	}

	ovirtClient := ovirtC.NewOvirtClient(connection)
	mScope := newMachineScope(ctx, ovirtClient, actuator.client, actuator.machinesClient, machine, providerSpec)
	if err := mScope.patchMachine(ctx, conditionSuccess()); err != nil {
		return actuator.handleMachineError(machine, "Update", apierrors.UpdateMachine(
			"Error patching Machine %v", err))
		return err
	}

	actuator.EventRecorder.Eventf(machine, corev1.EventTypeNormal, "Update", "Updated Machine %v", machine.Name)
	return nil
}

// Update deletes the VM from the RHV environment
func (actuator *OvirtActuator) Delete(ctx context.Context, machine *machinev1.Machine) error {
	connection, err := actuator.getConnection()
	if err != nil {
		return err
	}
	ovirtClient := ovirtC.NewOvirtClient(connection)
	mScope := newMachineScope(ctx, ovirtClient, actuator.client, actuator.machinesClient, machine, nil)
	if err := mScope.delete(); err != nil {
		return actuator.handleMachineError(machine, "Deleted", apierrors.UpdateMachine(
			"error deleting Ovirt instance %v", err))
		return err
	}
	actuator.EventRecorder.Eventf(machine, corev1.EventTypeNormal, "Deleted", "Deleted Machine %v", machine.Name)
	return nil
}

// If the OvirtActuator has a client for updating Machine objects, this will set
// the appropriate reason/message on the Machine.Status. If not, such as during
// cluster installation, it will operate as a no-op. It also returns the
// original error for convenience, so callers can do "return handleMachineError(...)".
func (actuator *OvirtActuator) handleMachineError(machine *machinev1.Machine, reason string, err *apierrors.MachineError) error {
	actuator.EventRecorder.Eventf(machine, corev1.EventTypeWarning, reason, "%v", err)

	if actuator.client != nil {
		machine.Status.ErrorReason = &err.Reason
		machine.Status.ErrorMessage = &err.Message
		if err := actuator.client.Update(context.TODO(), machine); err != nil {
			return fmt.Errorf("unable to update machine status: %v", err)
		}
	}

	klog.Errorf("Machine error %s: %v", machine.Name, err.Message)
	return err
}

// getConnection returns a a client to oVirt's API endpoint
func (actuator *OvirtActuator) getConnection() (*ovirtsdk.Connection, error) {
	var err error
	if actuator.ovirtConnection == nil || actuator.ovirtConnection.Test() != nil {
		creds, err := ovirtC.GetCredentialsSecret(actuator.client, utils.NAMESPACE, utils.OvirtCloudCredsSecretName)
		if err != nil {
			return nil, fmt.Errorf("failed getting credentials for namespace %s, %s", utils.NAMESPACE, err)
		}
		// session expired or some other error, re-login.
		actuator.ovirtConnection, err = ovirtC.CreateAPIConnection(creds)
		if err != nil {
			return nil, fmt.Errorf("failed getting credentials for namespace %s, %s", utils.NAMESPACE, err)
		}
	}
	return actuator.ovirtConnection, err
}

func conditionSuccess() ovirtconfigv1.OvirtMachineProviderCondition {
	return ovirtconfigv1.OvirtMachineProviderCondition{
		Type:    ovirtconfigv1.MachineCreated,
		Status:  corev1.ConditionTrue,
		Reason:  "MachineCreateSucceeded",
		Message: "Machine successfully created",
	}
}

// TODO: USE IT
func conditionFailed() ovirtconfigv1.OvirtMachineProviderCondition {
	return ovirtconfigv1.OvirtMachineProviderCondition{
		Type:    ovirtconfigv1.MachineCreated,
		Status:  corev1.ConditionFalse,
		Reason:  "MachineCreateFailed",
		Message: "Machine creation failed",
	}
}
