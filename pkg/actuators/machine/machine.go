package machine

import (
	"context"
	"fmt"

	osclientset "github.com/openshift/client-go/config/clientset/versioned"
	ovirtconfigv1 "github.com/openshift/cluster-api-provider-ovirt/pkg/apis/ovirtprovider/v1beta1"
	ovirtC "github.com/openshift/cluster-api-provider-ovirt/pkg/clients/ovirt"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/utils"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	"github.com/openshift/machine-api-operator/pkg/generated/clientset/versioned/typed/machine/v1beta1"
	"github.com/openshift/machine-api-operator/pkg/util"
	ovirtsdk "github.com/ovirt/go-ovirt"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	InstanceStatusAnnotationKey = "machine.openshift.io/instance-state"
	userDataSecretKey           = "userData"
)

type machineScope struct {
	context.Context
	ovirtClient         ovirtC.Client
	client              client.Client
	osClient            osclientset.Interface
	machine             *machinev1.Machine
	machinesClient      v1beta1.MachineV1beta1Interface
	machineProviderSpec *ovirtconfigv1.OvirtMachineProviderSpec
}

func newMachineScope(
	ctx context.Context,
	ovirtClient ovirtC.Client,
	client client.Client,
	machinesClient v1beta1.MachineV1beta1Interface,
	machine *machinev1.Machine,
	providerSpec *ovirtconfigv1.OvirtMachineProviderSpec) *machineScope {
	config := ctrl.GetConfigOrDie()
	osClient := osclientset.NewForConfigOrDie(rest.AddUserAgent(config, "cluster-api-provider-ovirt"))
	return &machineScope{
		Context:             ctx,
		ovirtClient:         ovirtClient,
		client:              client,
		machinesClient:      machinesClient,
		machine:             machine,
		machineProviderSpec: providerSpec,
		osClient:            osClient,
	}
}

// create creates an oVirt VM from the machine object if it does not exists.
func (ms *machineScope) create() error {
	// creating a new instance, we don't have the vm id yet
	instance, err := ms.ovirtClient.GetVMByName(ms.machine.Name)
	if err != nil {
		return err
	}
	// TODO: Handle that case in the actuator and make sure to return or at least check the impact on patch machine
	if instance != nil {
		klog.Infof("Skipped creating a VM that already exists.\n")
		return nil
	}

	ignition, err := ms.getIgnition()
	if err != nil {
		return apierrors.CreateMachine("Error getting VM ignition: %v", err)
	}

	instance, err = ms.ovirtClient.CreateVMByMachine(
		ms.machine.Name,
		ms.machine.Labels["machine.openshift.io/cluster-api-cluster"],
		ignition,
		ms.machineProviderSpec)
	if err != nil {
		return fmt.Errorf("Error creating Ovirt instance: %v", err)
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
		return fmt.Errorf("Error creating oVirt VM: %v", err)
	}

	// Start the VM
	err = ms.ovirtClient.StartVM(instance.MustId())
	if err != nil {
		return fmt.Errorf("Error running oVirt VM: %v", err)
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
		return fmt.Errorf("Error running oVirt VM: %v", err)
	}
	return nil
}

// exists returns true if machine exists.
func (ms *machineScope) exists() (bool, error) {
	instance, err := ms.ovirtClient.GetVMByMachine(*ms.machine)
	if err != nil {
		return false, err
	}
	return instance != nil, err
}

// delete deletes the VM which corresponds with the machine object from the oVirt engine
func (ms *machineScope) delete() error {
	instance, err := ms.ovirtClient.GetVMByMachine(*ms.machine)
	if err != nil {
		return err
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
		return nil, err
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
		return err
	}
	ms.reconcileMachineProviderID(instance)
	klog.V(5).Infof("Machine %s provider status %s", instance.MustName(), instance.MustStatus())

	err = ms.reconcileMachineNetwork(ctx, instance)
	if err != nil {
		return err
	}
	ms.reconcileMachineAnnotations(instance)
	err = ms.reconcileMachineProviderStatus(instance, condition)
	if err != nil {
		return err
	}

	// Copy the status, because its discarded and returned fresh from the DB by the machine resource update.
	// Save it for the status sub-resource update.
	statusCopy := *ms.machine.Status.DeepCopy()
	klog.Info("Updating machine resource")

	// TODO the namespace should be set on actuator creation. Remove the hardcoded openshift-machine-api.
	newMachine, err := ms.machinesClient.Machines("openshift-machine-api").Update(context.TODO(), ms.machine, metav1.UpdateOptions{})
	if err != nil {
		return err
	}

	newMachine.Status = statusCopy
	klog.Info("Updating machine status sub-resource")
	if _, err := ms.machinesClient.Machines("openshift-machine-api").UpdateStatus(context.TODO(), newMachine, metav1.UpdateOptions{}); err != nil {
		return err
	}
	return nil
}

func (ms *machineScope) reconcileMachineNetwork(ctx context.Context, instance *ovirtC.Instance) error {
	switch instance.MustStatus() {
	// expect IP addresses only on those statuses.
	// in those statuses we 'll try reconciling
	case ovirtsdk.VMSTATUS_UP, ovirtsdk.VMSTATUS_MIGRATING:
		break

	// update machine status.
	case ovirtsdk.VMSTATUS_DOWN:
		return nil

	// return error if vm is transient state this will force retry reconciling until VM is up.
	// there is no event generated that will trigger this.  BZ1854787
	default:
		return fmt.Errorf("Aborting reconciliation while VM %s  state is %s", instance.MustName(), instance.MustStatus())

	}
	name := instance.MustName()
	addresses := []corev1.NodeAddress{{Address: name, Type: corev1.NodeInternalDNS}}

	vmID := instance.MustId()
	klog.V(5).Infof("using oVirt SDK to find %s IP addresses", name)

	// get API and ingress addresses that will be excluded from the node address selection
	excludeAddr, err := ms.getClusterAddress(ctx)
	if err != nil {
		return err
	}

	ip, err := ms.ovirtClient.FindVirtualMachineIP(vmID, excludeAddr)

	if err != nil {
		// stop reconciliation till we get IP addresses - otherwise the state will be considered stable.
		klog.Errorf("failed to lookup the VM IP %s - skip setting addresses for this machine", err)
		return err
	} else {
		klog.V(5).Infof("received IP address %v from engine", ip)
		addresses = append(addresses, corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: ip})
	}
	ms.machine.Status.Addresses = addresses
	return nil
}

func (ms *machineScope) getClusterAddress(ctx context.Context) (map[string]int, error) {
	infra, err := ms.osClient.ConfigV1().Infrastructures().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		klog.Error(err, "Failed to retrieve Cluster details")
		return nil, err
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
		return err
	}
	providerStatus.InstanceState = &status
	providerStatus.InstanceID = &name
	providerStatus.Conditions = ms.reconcileMachineConditions(providerStatus.Conditions, condition)
	rawExtension, err := ovirtconfigv1.RawExtensionFromProviderStatus(providerStatus)
	if err != nil {
		return err
	}
	ms.machine.Status.ProviderStatus = rawExtension
	return nil
}

func (ms *machineScope) reconcileMachineProviderID(instance *ovirtC.Instance) {
	id := instance.MustId()
	providerID := utils.ProviderIDPrefix + id
	ms.machine.Spec.ProviderID = &providerID

	if ms.machine.ObjectMeta.Annotations == nil {
		ms.machine.ObjectMeta.Annotations = make(map[string]string)
	}
	ms.machine.ObjectMeta.Annotations[utils.OvirtIDAnnotationKey] = id
}

func (ms *machineScope) reconcileMachineAnnotations(instance *ovirtC.Instance) {
	if ms.machine.ObjectMeta.Annotations == nil {
		ms.machine.ObjectMeta.Annotations = make(map[string]string)
	}
	ms.machine.ObjectMeta.Annotations[InstanceStatusAnnotationKey] = string(instance.MustStatus())
}

func (ms *machineScope) reconcileMachineConditions(
	conditions []ovirtconfigv1.OvirtMachineProviderCondition,
	newCondition ovirtconfigv1.OvirtMachineProviderCondition) []ovirtconfigv1.OvirtMachineProviderCondition {
	if conditions == nil {
		now := metav1.Now()
		newCondition.LastProbeTime = now
		newCondition.LastTransitionTime = now
		return []ovirtconfigv1.OvirtMachineProviderCondition{newCondition}
	}

	for _, c := range conditions {
		if c.Type == newCondition.Type {
			if c.Reason != newCondition.Reason || c.Message != newCondition.Message {
				if c.Status != newCondition.Status {
					newCondition.LastTransitionTime = metav1.Now()
				}
				c.Status = newCondition.Status
				c.Message = newCondition.Message
				c.Reason = newCondition.Reason
				c.LastProbeTime = newCondition.LastProbeTime
				return conditions
			}
		}
	}
	return conditions
}
