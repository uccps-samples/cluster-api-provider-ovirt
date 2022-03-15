package machine

import (
	"context"
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	ovirtconfigv1 "github.com/openshift/cluster-api-provider-ovirt/pkg/apis/ovirtprovider/v1beta1"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/utils"
	ovirtC "github.com/ovirt/go-ovirt-client"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
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

	//TODO: replace to GetVMByName once implemented in the client
	vms, err := ms.ovirtClient.SearchVMs(ovirtC.VMSearchParams().WithName(ms.machine.Name))
	// creating a new instance, we don't have the vm id yet
	if err != nil {
		return errors.Wrap(err, "error finding VM by name")
	}
	if vms != nil {
		klog.Infof("Skipped creating a VM that already exists.\n")
		return nil
	}

	ignition, err := ms.getIgnition()
	if err != nil {
		return errors.Wrap(err, "error getting VM ignition")
	}

	optionalVMParams := ovirtC.CreateVMParams().MustWithInitializationParameters(string(ignition), "test-vm")
	clusterName := ms.machine.Labels["machine.openshift.io/cluster-api-cluster"]

	temp, _ := ms.ovirtClient.GetBlankTemplate() //TODO: replace to GetTemplateByName once implemented in the client

	instance, err := ms.ovirtClient.CreateVM(clusterName,
		temp.ID(), //TODO: replace to GetTemplateByName once implemented in the client
		ms.machine.Name,
		optionalVMParams)

	if err != nil {
		return errors.Wrap(err, "error creating Ovirt instance")
	}

	// Wait till ready
	_, err = ms.ovirtClient.WaitForVMStatus(instance.ID(), ovirtC.VMStatusDown)
	if err != nil {
		return errors.Wrap(err, "error creating oVirt VM")
	}

	// Start the VM
	err = ms.ovirtClient.StartVM(instance.ID())
	if err != nil {
		return errors.Wrap(err, "error running oVirt VM")
	}

	// Wait till running
	_, err = ms.ovirtClient.WaitForVMStatus(instance.ID(), ovirtC.VMStatusUp)
	if err != nil {
		return errors.Wrap(err, "Error waiting for oVirt VM to be UP")
	}
	return nil
}

// exists returns true if machine exists.
func (ms *machineScope) exists() (bool, error) {
	//TODO: replace to GetVMByName once implemented in the client
	vms, err := ms.ovirtClient.SearchVMs(ovirtC.VMSearchParams().WithName(ms.machine.Name))

	if err != nil {
		return false, errors.Wrap(err, "error finding VM by name")
	}
	return vms != nil, nil
}

// delete deletes the VM which corresponds with the machine object from the oVirt engine
func (ms *machineScope) delete() error {
	//TODO: replace to GetVMByName once implemented in the client
	vms, err := ms.ovirtClient.SearchVMs(ovirtC.VMSearchParams().WithName(ms.machine.Name))

	if err != nil {
		return errors.Wrap(err, "error finding VM by name")
	}

	if vms == nil {
		klog.Infof("Skipped deleting a VM that is already deleted.\n")
		return nil
	}

	return ms.ovirtClient.RemoveVM(vms[0].ID())
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

func (ms *machineScope) reconcileMachine(ctx context.Context) error {
	//TODO: replace to GetVMByName once implemented in the client
	vms, err := ms.ovirtClient.SearchVMs(ovirtC.VMSearchParams().WithName(ms.machine.Name))

	if err != nil {
		return errors.Wrap(err, "error finding VM by name")
	}

	instance := vms[0]

	id := instance.ID()
	status := instance.Status()
	name := instance.Name()
	ms.reconcileMachineProviderID(id)
	ms.reconcileMachineAnnotations(string(status), id)
	err = ms.reconcileMachineNetwork(ctx, status, name, id)
	if err != nil {
		return errors.Wrap(err, "error reconciling machine network")
	}
	err = ms.reconcileMachineProviderStatus(string(status), &id)
	if err != nil {
		return errors.Wrap(err, "error reconciling machine provider status")
	}
	return nil
}

func (ms *machineScope) patchMachine(ctx context.Context) error {
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

func (ms *machineScope) reconcileMachineNetwork(ctx context.Context, status ovirtC.VMStatus,
	name string, vmID string) error {
	switch status {
	// expect IP addresses only on those statuses.
	// in those statuses we 'll try reconciling
	case ovirtC.VMStatusUp, ovirtC.VMStatusMigrating:
	// Do nothing, we can proceed to reconcile Network
	// update machine status.
	// TODO: Should we clean the addresses here?
	case ovirtC.VMStatusDown:
		return nil

	// return error if vm is transient state this will force retry reconciling until VM is up.
	// there is no event generated that will trigger this.  BZ1854787
	default:
		return fmt.Errorf("requeuing reconciliation, VM %s state is %s", name, status)
	}
	addresses := []corev1.NodeAddress{{Address: name, Type: corev1.NodeInternalDNS}}

	klog.V(5).Infof("using oVirt SDK to find %s IP addresses", name)

	// get API and ingress addresses that will be excluded from the node address selection
	//excludeAddr, err := ms.getClusterAddress(ctx)
	//if err != nil {
	//	return errors.Wrap(err, "error getting cluster address")
	//}

	// TODO: implement FindVirtualMachineIP in the client
	ip := "192.168.210.11"
	var err error = nil

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

func (ms *machineScope) reconcileMachineProviderStatus(status string, id *string) error {
	providerStatus, err := ovirtconfigv1.ProviderStatusFromRawExtension(ms.machine.Status.ProviderStatus)
	if err != nil {
		return errors.Wrap(err, "error unmarshaling machine ProviderStatus field")
	}
	providerStatus.InstanceState = &status
	providerStatus.InstanceID = id
	rawExtension, err := ovirtconfigv1.RawExtensionFromProviderStatus(providerStatus)
	if err != nil {
		return errors.Wrap(err, "error marshaling machine ProviderStatus field")
	}
	ms.machine.Status.ProviderStatus = rawExtension
	return nil
}

func (ms *machineScope) reconcileMachineProviderID(id string) {
	providerID := utils.ProviderIDPrefix + id
	ms.machine.Spec.ProviderID = &providerID
}

func (ms *machineScope) reconcileMachineAnnotations(status string, id string) {
	if ms.machine.ObjectMeta.Annotations == nil {
		ms.machine.ObjectMeta.Annotations = make(map[string]string)
	}
	ms.machine.ObjectMeta.Annotations[InstanceStatusAnnotationKey] = status
	ms.machine.ObjectMeta.Annotations[utils.OvirtIDAnnotationKey] = id
}
