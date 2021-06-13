package machine

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	ovirtconfigv1 "github.com/openshift/cluster-api-provider-ovirt/pkg/apis/ovirtprovider/v1beta1"
	ovirtC "github.com/openshift/cluster-api-provider-ovirt/pkg/clients/ovirt"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/utils"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	"github.com/openshift/machine-api-operator/pkg/util"
	ovirtsdk "github.com/ovirt/go-ovirt"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	InstanceStatusAnnotationKey = "machine.openshift.io/instance-state"
	userDataSecretKey           = "userData"
	// GlobalInfrastuctureName default name for infrastructure object
	globalInfrastuctureName = "cluster"
)

type machineScope struct {
	context.Context
	ovirtClient ovirtC.Client
	client      client.Client
	machine     *machinev1.Machine
	// originalMachineToBePatched contains a patch copy of the machine when the machine scope was created
	// it is used by k8sclient to understand the diff and patch the machine object
	originalMachineToBePatched client.Patch
	machineProviderSpec        *ovirtconfigv1.OvirtMachineProviderSpec
}

func newMachineScope(
	ctx context.Context,
	ovirtClient ovirtC.Client,
	c client.Client,
	machine *machinev1.Machine,
	providerSpec *ovirtconfigv1.OvirtMachineProviderSpec) *machineScope {

	return &machineScope{
		Context:                    ctx,
		ovirtClient:                ovirtClient,
		client:                     c,
		machine:                    machine,
		originalMachineToBePatched: client.MergeFrom(machine.DeepCopy()),
		machineProviderSpec:        providerSpec,
	}
}

// create creates an oVirt VM from the machine object if it does not exists.
func (ms *machineScope) create() error {
	// creating a new instance, we don't have the vm id yet
	instance, err := ms.ovirtClient.GetVMByName(ms.machine.Name)
	if err != nil {
		return errors.Wrap(err, "error finding VM by name")
	}
	// TODO: Handle that case in the actuator and make sure to return or at least check the impact on patch machine
	if instance != nil {
		klog.Infof("Skipped creating a VM that already exists.\n")
		return nil
	}

	ignition, err := ms.getIgnition()
	if err != nil {
		return errors.Wrap(err, "error getting VM ignition")
	}

	instance, err = ms.ovirtClient.CreateVMByMachine(
		ms.machine.Name,
		ms.machine.Labels["machine.openshift.io/cluster-api-cluster"],
		ignition,
		ms.machineProviderSpec)
	if err != nil {
		return errors.Wrap(err, "error creating Ovirt instance")
	}

	// Wait till ready
	// TODO: export to a regular function
	// TODO: Why are we always setting error to nil?!
	err = util.PollImmediate(retryIntervalInstanceStatus, timeoutInstanceCreate, func() (bool, error) {
		instance, err := ms.ovirtClient.GetVMByMachine(*ms.machine)
		if err != nil {
			return false, nil
		}
		return instance.MustStatus() == ovirtsdk.VMSTATUS_DOWN, nil
	})
	if err != nil {
		return errors.Wrap(err, "error creating oVirt VM")
	}

	// Start the VM
	err = ms.ovirtClient.StartVM(instance.MustId())
	if err != nil {
		return errors.Wrap(err, "error running oVirt VM")
	}

	// Wait till running
	// TODO: export to a regular function
	// TODO: Why are we always setting error to nil?!
	err = util.PollImmediate(retryIntervalInstanceStatus, timeoutInstanceCreate, func() (bool, error) {
		instance, err := ms.ovirtClient.GetVMByMachine(*ms.machine)
		if err != nil {
			return false, nil
		}
		return instance.MustStatus() == ovirtsdk.VMSTATUS_UP, nil
	})
	if err != nil {
		return errors.Wrap(err, "Error running oVirt VM")
	}
	return nil
}

// exists returns true if machine exists.
func (ms *machineScope) exists() (bool, error) {
	instance, err := ms.ovirtClient.GetVMByMachine(*ms.machine)
	if err != nil {
		return false, errors.Wrap(err, "error finding VM by name")
	}
	return instance != nil, nil
}

// delete deletes the VM which corresponds with the machine object from the oVirt engine
func (ms *machineScope) delete() error {
	instance, err := ms.ovirtClient.GetVMByMachine(*ms.machine)
	if err != nil {
		return errors.Wrap(err, "error finding VM by name")
	}
	if instance == nil {
		klog.Infof("Skipped deleting a VM that is already deleted.\n")
		return nil
	}
	return ms.ovirtClient.DeleteVM(instance.MustId())
}

// returns the ignition from the userData secret
// Ignition is the utility that is used by RHCOS to manipulate disks during initial configuration.
// Ignition completes common disk tasks, including partitioning disks, formatting partitions, writing files,
// and configuring users. For more details see Openshift/RHCOS Docs
func (ms *machineScope) getIgnition() ([]byte, error) {
	if ms.machineProviderSpec == nil || ms.machineProviderSpec.UserDataSecret == nil {
		return nil, nil
	}
	userDataSecret := &corev1.Secret{}
	objKey := client.ObjectKey{
		Namespace: ms.machine.Namespace,
		Name:      ms.machineProviderSpec.UserDataSecret.Name,
	}
	if err := ms.client.Get(ms.Context, objKey, userDataSecret); err != nil {
		return nil, errors.Wrap(err, "error getting userDataSecret")
	}
	userData, exists := userDataSecret.Data[userDataSecretKey]
	if !exists {
		return nil, fmt.Errorf("secret %s missing %s key", objKey, userDataSecretKey)
	}
	return userData, nil
}

func (ms *machineScope) patchMachine(ctx context.Context, condition ovirtconfigv1.OvirtMachineProviderCondition) error {
	instance, err := ms.ovirtClient.GetVMByMachine(*ms.machine)
	if err != nil {
		return errors.Wrap(err, "error finding VM by name")
	}
	ms.reconcileMachineProviderID(instance)
	klog.V(5).Infof("Machine %s provider status %s", instance.MustName(), instance.MustStatus())

	err = ms.reconcileMachineNetwork(ctx, instance)
	if err != nil {
		return errors.Wrap(err, "error reconciling machine network")
	}
	ms.reconcileMachineAnnotations(instance)
	err = ms.reconcileMachineProviderStatus(instance, condition)
	if err != nil {
		return errors.Wrap(err, "error reconciling machine provider status")
	}

	// Copy the status, because its discarded and returned fresh from the DB by the machine resource update.
	// Save it for the status sub-resource update.
	statusCopy := *ms.machine.Status.DeepCopy()
	klog.Info("Updating machine resource")

	if err := ms.client.Patch(ctx, ms.machine, ms.originalMachineToBePatched); err != nil {
		klog.Errorf("Failed to patch machine %q: %v", ms.machine.GetName(), err)
		return err
	}

	ms.machine.Status = statusCopy

	// patch status
	klog.Info("Updating machine status sub-resource")
	if err := ms.client.Status().Patch(ctx, ms.machine, ms.originalMachineToBePatched); err != nil {
		klog.Errorf("Failed to patch machine status %q: %v", ms.machine.GetName(), err)
		return err
	}
	return nil
}

func (ms *machineScope) reconcileMachineNetwork(ctx context.Context, instance *ovirtC.Instance) error {
	switch instance.MustStatus() {
	// expect IP addresses only on those statuses.
	// in those statuses we 'll try reconciling
	case ovirtsdk.VMSTATUS_UP, ovirtsdk.VMSTATUS_MIGRATING:
	// update machine status.
	case ovirtsdk.VMSTATUS_DOWN:
		return nil

	// return error if vm is transient state this will force retry reconciling until VM is up.
	// there is no event generated that will trigger this.  BZ1854787
	default:
		return fmt.Errorf(
			"requeuing reconciliation, VM %s state is %s", instance.MustName(), instance.MustStatus())
	}
	name := instance.MustName()
	addresses := []corev1.NodeAddress{{Address: name, Type: corev1.NodeInternalDNS}}

	vmID := instance.MustId()
	klog.V(5).Infof("using oVirt SDK to find %s IP addresses", name)

	// get API and ingress addresses that will be excluded from the node address selection
	excludeAddr, err := ms.getClusterAddress(ctx)
	if err != nil {
		return errors.Wrap(err, "error getting cluster address")
	}

	ip, err := ms.ovirtClient.FindVirtualMachineIP(vmID, excludeAddr)

	if err != nil {
		// stop reconciliation till we get IP addresses - otherwise the state will be considered stable.
		klog.Errorf("failed to lookup the VM IP %s - skip setting addresses for this machine", err)
		return errors.Wrap(
			err, "failed to lookup the VM IP - skip setting addresses for this machine")
	}
	klog.V(5).Infof("received IP address %v from engine", ip)
	addresses = append(addresses, corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: ip})
	ms.machine.Status.Addresses = addresses
	return nil
}

func (ms *machineScope) getClusterAddress(ctx context.Context) (map[string]int, error) {
	infra := &configv1.Infrastructure{}
	objKey := client.ObjectKey{Name: globalInfrastuctureName}
	if err := ms.client.Get(ctx, objKey, infra); err != nil {
		return nil, errors.Wrap(err, "error getting infrastucture data")
	}

	var clusterAddr = make(map[string]int)
	clusterAddr[infra.Status.PlatformStatus.Ovirt.APIServerInternalIP] = 1
	clusterAddr[infra.Status.PlatformStatus.Ovirt.IngressIP] = 1

	return clusterAddr, nil
}

func (ms *machineScope) reconcileMachineProviderStatus(instance *ovirtC.Instance, condition ovirtconfigv1.OvirtMachineProviderCondition) error {
	status := string(instance.MustStatus())
	name := instance.MustId()

	providerStatus, err := ovirtconfigv1.ProviderStatusFromRawExtension(ms.machine.Status.ProviderStatus)
	if err != nil {
		return errors.Wrap(err, "error unmarshaling machine ProviderStatus field")
	}
	providerStatus.InstanceState = &status
	providerStatus.InstanceID = &name
	providerStatus.Conditions = ms.reconcileMachineConditions(providerStatus.Conditions, condition)
	rawExtension, err := ovirtconfigv1.RawExtensionFromProviderStatus(providerStatus)
	if err != nil {
		return errors.Wrap(err, "error marshaling machine ProviderStatus field")
	}
	ms.machine.Status.ProviderStatus = rawExtension
	return nil
}

func (ms *machineScope) reconcileMachineProviderID(instance *ovirtC.Instance) {
	id := instance.MustId()
	providerID := utils.ProviderIDPrefix + id
	ms.machine.Spec.ProviderID = &providerID

}

func (ms *machineScope) reconcileMachineAnnotations(instance *ovirtC.Instance) {
	if ms.machine.ObjectMeta.Annotations == nil {
		ms.machine.ObjectMeta.Annotations = make(map[string]string)
	}
	ms.machine.ObjectMeta.Annotations[InstanceStatusAnnotationKey] = string(instance.MustStatus())
	ms.machine.ObjectMeta.Annotations[utils.OvirtIDAnnotationKey] = instance.MustId()
}

// reconcileMachineConditions returns the conditions that will be set for the machine
// If the machine does not already have a condition with the specified type, a condition will be added to the slice
// If the machine does already have a condition with the specified type, the condition will be updated
func (ms *machineScope) reconcileMachineConditions(
	currentConditions []ovirtconfigv1.OvirtMachineProviderCondition,
	newCondition ovirtconfigv1.OvirtMachineProviderCondition) []ovirtconfigv1.OvirtMachineProviderCondition {

	if existingCondition := findProviderConditionByType(currentConditions, newCondition.Type); existingCondition == nil {
		// If there is no condition with the same type then add the new condition
		now := metav1.Now()
		newCondition.LastProbeTime = now
		newCondition.LastTransitionTime = now
		currentConditions = append(currentConditions, newCondition)
	} else {
		updateExistingCondition(&newCondition, existingCondition)
	}
	return currentConditions
}

func findProviderConditionByType(conditions []ovirtconfigv1.OvirtMachineProviderCondition, conditionType ovirtconfigv1.OvirtMachineProviderConditionType) *ovirtconfigv1.OvirtMachineProviderCondition {
	for i := range conditions {
		if conditions[i].Type == conditionType {
			return &conditions[i]
		}
	}
	return nil
}

func updateExistingCondition(newCondition, existingCondition *ovirtconfigv1.OvirtMachineProviderCondition) {
	if !shouldUpdateCondition(newCondition, existingCondition) {
		return
	}

	if existingCondition.Status != newCondition.Status {
		existingCondition.LastTransitionTime = metav1.Now()
	}
	existingCondition.Status = newCondition.Status
	existingCondition.Reason = newCondition.Reason
	existingCondition.Message = newCondition.Message
	existingCondition.LastProbeTime = newCondition.LastProbeTime
}

func shouldUpdateCondition(newCondition, existingCondition *ovirtconfigv1.OvirtMachineProviderCondition) bool {
	return newCondition.Reason != existingCondition.Reason || newCondition.Message != existingCondition.Message
}
