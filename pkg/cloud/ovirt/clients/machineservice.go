/*
Copyright oVirt Authors
SPDX-License-Identifier: Apache-2.0
*/

package clients

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"time"

	"github.com/pkg/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog"

	ovirtsdk "github.com/ovirt/go-ovirt"

	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	"github.com/openshift/machine-api-operator/pkg/util"

	ovirtconfigv1 "github.com/openshift/cluster-api-provider-ovirt/pkg/apis/ovirtprovider/v1beta1"
)

type InstanceService struct {
	Connection   *ovirtsdk.Connection
	ClusterId    string
	TemplateName string
	MachineName  string
}

type Instance struct {
	*ovirtsdk.Vm
}

type SshKeyPair struct {
	Name string `json:"name"`

	// PublicKey is the public key from this pair, in OpenSSH format.
	// "ssh-rsa AAAAB3Nz..."
	PublicKey string `json:"public_key"`

	// PrivateKey is the private key from this pair, in PEM format.
	// "-----BEGIN RSA PRIVATE KEY-----\nMIICXA..."
	// It is only present if this KeyPair was just returned from a Create call.
	PrivateKey string `json:"private_key"`
}

type InstanceListOpts struct {
	Name string `json:"name"`
}

func NewInstanceServiceFromMachine(machine *machinev1.Machine, connection *ovirtsdk.Connection) (*InstanceService, error) {
	machineSpec, err := ovirtconfigv1.MachineSpecFromProviderSpec(machine.Spec.ProviderSpec)
	if err != nil {
		return nil, err
	}

	service := &InstanceService{Connection: connection}
	service.ClusterId = machineSpec.ClusterId
	service.TemplateName = machineSpec.TemplateName
	service.MachineName = machine.Name
	return service, err
}

func (is *InstanceService) InstanceCreate(
	machine *machinev1.Machine,
	providerSpec *ovirtconfigv1.OvirtMachineProviderSpec,
	kubeClient *kubernetes.Clientset) (instance *Instance, err error) {

	if providerSpec == nil {
		return nil, fmt.Errorf("create Options need be specified to create instace")
	}

	userDataSecret, err := kubeClient.CoreV1().
		Secrets(machine.Namespace).
		Get(context.TODO(), providerSpec.UserDataSecret.Name, v1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user data secret for the machine namespace: %s", err)
	}

	ignition, ok := userDataSecret.Data["userData"]
	if !ok {
		return nil, fmt.Errorf("failed extracting ignition from user data secret %v", string(ignition))
	}
	cluster := ovirtsdk.NewClusterBuilder().Id(providerSpec.ClusterId).MustBuild()
	template := ovirtsdk.NewTemplateBuilder().Name(providerSpec.TemplateName).MustBuild()
	init := ovirtsdk.NewInitializationBuilder().
		CustomScript(string(ignition)).
		HostName(machine.Name).
		MustBuild()

	vmBuilder := ovirtsdk.NewVmBuilder().
		Name(machine.Name).
		Cluster(cluster).
		Template(template).
		Initialization(init)

	if providerSpec.VMType != "" {
		vmBuilder.Type(ovirtsdk.VmType(providerSpec.VMType))
	}
	if providerSpec.InstanceTypeId != "" {
		vmBuilder.InstanceTypeBuilder(
			ovirtsdk.NewInstanceTypeBuilder().
				Id(providerSpec.InstanceTypeId))
	} else {
		if providerSpec.CPU != nil {
			vmBuilder.CpuBuilder(
				ovirtsdk.NewCpuBuilder().
					TopologyBuilder(ovirtsdk.NewCpuTopologyBuilder().
						Cores(int64(providerSpec.CPU.Cores)).
						Sockets(int64(providerSpec.CPU.Sockets)).
						Threads(int64(providerSpec.CPU.Threads))))
		}
		if providerSpec.MemoryMB > 0 {
			vmBuilder.Memory(int64(math.Pow(2, 20)) * int64(providerSpec.MemoryMB))
		}
	}

	isAutoPinning := false
	if providerSpec.AutoPinningPolicy != "" {
		autoPinningPolicy := mapAutoPinningPolicy(providerSpec.AutoPinningPolicy)

		// if we have a policy, we need to set the pinning to all the hosts in the cluster.
		if autoPinningPolicy != ovirtsdk.AUTOPINNINGPOLICY_DISABLED {
			isAutoPinning = true
			hostsInCluster, err := is.getHostsInCluster(providerSpec.ClusterId)
			if err != nil {
				return nil, err
			}
			placementPolicyBuilder := ovirtsdk.NewVmPlacementPolicyBuilder()
			placementPolicy, err := placementPolicyBuilder.Hosts(hostsInCluster).
				Affinity(ovirtsdk.VMAFFINITY_MIGRATABLE).Build()
			if err != nil {
				return nil, fmt.Errorf("failed to build the placement policy of the vm: %v", err)
			}
			vmBuilder.PlacementPolicy(placementPolicy)
		}
	}
	if providerSpec.Hugepages > 0 {
		customProp, err := ovirtsdk.NewCustomPropertyBuilder().
			Name("hugepages").
			Value(fmt.Sprint(providerSpec.Hugepages)).
			Build()
		if err != nil {
			return nil, err
		}
		vmBuilder.CustomPropertiesOfAny(customProp)
	}

	vm, err := vmBuilder.Build()
	if err != nil {
		return nil, errors.Wrap(err, "failed to construct VM struct")
	}

	klog.Infof("creating VM: %v", vm.MustName())
	response, err := is.Connection.SystemService().VmsService().Add().Vm(vm).Send()
	if err != nil {
		klog.Errorf("Failed creating VM %v", err)
		return nil, err
	}

	vmID := response.MustVm().MustId()

	ovirtClusterID := machine.Labels["machine.openshift.io/cluster-api-cluster"]

	err = is.Connection.WaitForVM(vmID, ovirtsdk.VMSTATUS_DOWN, time.Minute)
	if err != nil {
		return nil, errors.Wrap(err, "timed out waiting for the VM creation to finish")
	}

	vmService := is.Connection.SystemService().VmsService().VmService(vmID)

	if providerSpec.OSDisk != nil {
		err = is.handleDiskExtension(vmService, response, providerSpec)
		if err != nil {
			return nil, err
		}
	}

	err = is.handleNics(vmService, providerSpec)
	if err != nil {
		return nil, errors.Wrapf(err, "failed handling nics creation for VM %s", vm.MustName())
	}

	if isAutoPinning {
		err = is.handleAutoPinning(vmID, mapAutoPinningPolicy(providerSpec.AutoPinningPolicy))
		if err != nil {
			klog.Errorf("updating the VM (%s) with auto pinning policy failed! %v", vmID, err)
		}
	}

	_, err = is.Connection.SystemService().VmsService().
		VmService(response.MustVm().MustId()).
		TagsService().Add().
		Tag(ovirtsdk.NewTagBuilder().Name(ovirtClusterID).MustBuild()).
		Send()
	if err != nil {
		klog.Errorf("Failed to add tag to VM, skipping %v", err)
	}

	err = is.handleAffinityGroups(
		response.MustVm(),
		providerSpec.ClusterId,
		providerSpec.AffinityGroupsNames)
	if err != nil {
		return nil, err
	}
	return &Instance{response.MustVm()}, nil
}

func (is *InstanceService) handleDiskExtension(vmService *ovirtsdk.VmService, createdVM *ovirtsdk.VmsServiceAddResponse, providerSpec *ovirtconfigv1.OvirtMachineProviderSpec) error {
	attachmentsResponse, err := vmService.DiskAttachmentsService().List().Send()
	if err != nil {
		return err
	}

	var bootableDiskAttachment *ovirtsdk.DiskAttachment
	for _, disk := range attachmentsResponse.MustAttachments().Slice() {
		if disk.MustBootable() {
			// found the os disk
			bootableDiskAttachment = disk
		}
	}
	if bootableDiskAttachment == nil {
		return fmt.Errorf("the VM %s(%s) doesn't have a bootable disk - was Blank template used by mistake?",
			createdVM.MustVm().MustName(), createdVM.MustVm().MustId())
	}
	// extend the disk if requested size is bigger than template. We won't support shrinking it.
	newDiskSize := providerSpec.OSDisk.SizeGB * int64(math.Pow(2, 30))

	// get the disk
	getDisk, err := vmService.Connection().SystemService().DisksService().DiskService(bootableDiskAttachment.MustId()).Get().Send()
	if err != nil {
		return err
	}

	size := getDisk.MustDisk().MustProvisionedSize()
	if newDiskSize < size {
		klog.Warning("The machine spec specified new disk size %d, and the current disk size is %d. Shrinking is "+
			"not supported.", newDiskSize, size)
	}
	if newDiskSize > size {
		klog.Infof("Extending the OS disk from %d to %d", size, newDiskSize)
		bootableDiskAttachment.SetDisk(getDisk.MustDisk())
		bootableDiskAttachment.
			MustDisk().
			SetProvisionedSize(newDiskSize)
		_, err := vmService.DiskAttachmentsService().
			AttachmentService(bootableDiskAttachment.MustId()).
			Update().
			DiskAttachment(bootableDiskAttachment).
			Send()
		if err != nil {
			return fmt.Errorf("failed to update the OS disk - %s", err)
		}
		klog.Infof("Waiting while extending the OS disk")
		// wait for the disk extension to be over
		err = is.Connection.WaitForDisk(bootableDiskAttachment.MustId(), ovirtsdk.DISKSTATUS_OK, 20*time.Minute)
		if err != nil {
			return err
		}
	}
	return nil
}

func (is *InstanceService) InstanceDelete(id string) error {
	klog.Infof("Deleting VM with ID: %s", id)
	vmService := is.Connection.SystemService().VmsService().VmService(id)
	_, err := vmService.Stop().Send()
	if err != nil {
		return err
	}
	err = util.PollImmediate(time.Second*10, time.Minute*5, func() (bool, error) {
		vmResponse, err := vmService.Get().Send()
		if err != nil {
			return false, nil
		}
		vm, ok := vmResponse.Vm()
		if !ok {
			return false, err
		}
		return vm.MustStatus() == ovirtsdk.VMSTATUS_DOWN, nil
	})
	if err != nil {
		return fmt.Errorf("error waiting for VM %s to be down: %w", id, err)
	}
	// Detach all the disks that are not the bootable disk, to prevent them from being deleted
	diskAttachmentsResp, err := vmService.DiskAttachmentsService().List().Send()
	diskAttachments, ok := diskAttachmentsResp.Attachments()
	if !ok {
		return fmt.Errorf("error getting attachments from vm %s, error is: %w", id, err)
	}
	for _, da := range diskAttachments.Slice() {
		bootable, ok := da.Bootable()
		if !ok {
			return fmt.Errorf("error while checking if disk attachment for vm %s is bootable: %w", id, err)
		}
		if !bootable {
			_, err = vmService.DiskAttachmentsService().AttachmentService(da.MustId()).Remove().Send()
			if err != nil {
				return fmt.Errorf("error while detaching disk attachment %s for vm %s : %w", da.MustId(), id, err)
			}
		}
	}
	_, err = vmService.Remove().Send()

	// poll till VM doesn't exist
	err = util.PollImmediate(time.Second*10, time.Minute*5, func() (bool, error) {
		_, err := vmService.Get().Send()
		return err != nil, nil
	})
	return err
}

// Get VM by ID or Name
func (is *InstanceService) GetVm(machine machinev1.Machine) (instance *Instance, err error) {
	if machine.Spec.ProviderID != nil && *machine.Spec.ProviderID != "" {
		instance, err = is.GetVmByID(*machine.Spec.ProviderID)
		if err == nil {
			return instance, err
		}
	}
	instance, err = is.GetVmByName()
	return instance, err

}

func (is *InstanceService) GetVmByID(resourceId string) (instance *Instance, err error) {
	klog.Infof("Fetching VM by ID: %s", resourceId)
	if resourceId == "" {
		return nil, fmt.Errorf("resourceId should be specified to get detail")
	}
	response, err := is.Connection.SystemService().VmsService().VmService(resourceId).Get().Send()
	if err != nil {
		return nil, err
	}
	klog.Infof("Got VM by ID: %s", response.MustVm().MustName())
	return &Instance{Vm: response.MustVm()}, nil
}

func (is *InstanceService) GetVmByName() (*Instance, error) {
	response, err := is.Connection.SystemService().VmsService().
		List().Search("name=" + is.MachineName).Send()
	if err != nil {
		klog.Errorf("Failed to fetch VM by name")
		return nil, err
	}
	for _, vm := range response.MustVms().Slice() {
		if name, ok := vm.Name(); ok {
			if name == is.MachineName {
				return &Instance{Vm: vm}, nil
			}
		}
	}
	// returning an nil instance if we didn't find a match
	return nil, nil
}

func (is *InstanceService) handleNics(vmService *ovirtsdk.VmService, spec *ovirtconfigv1.OvirtMachineProviderSpec) error {
	if spec.NetworkInterfaces == nil || len(spec.NetworkInterfaces) == 0 {
		return nil
	}
	nicList, err := vmService.NicsService().List().Send()
	if err != nil {
		return errors.Wrap(err, "failed fetching VM network interfaces")
	}

	// remove all existing nics
	for _, n := range nicList.MustNics().Slice() {
		_, err := vmService.NicsService().NicService(n.MustId()).Remove().Send()
		if err != nil {
			return errors.Wrap(err, "failed clearing all interfaces before populating new ones")
		}
	}

	// re-add nics
	for i, nic := range spec.NetworkInterfaces {
		_, err := vmService.NicsService().Add().Nic(
			ovirtsdk.NewNicBuilder().
				Name(fmt.Sprintf("nic%d", i+1)).
				VnicProfileBuilder(ovirtsdk.NewVnicProfileBuilder().Id(nic.VNICProfileID)).
				MustBuild()).
			Send()
		if err != nil {
			return errors.Wrap(err, "failed to create network interface")
		}
	}
	return nil
}

//Find virtual machine IP Address by ID
func (is *InstanceService) FindVirtualMachineIP(id string, excludeAddr map[string]int) (string, error) {

	vmService := is.Connection.SystemService().VmsService().VmService(id)

	// Get the guest reported devices
	reportedDeviceResp, err := vmService.ReportedDevicesService().List().Send()
	if err != nil {
		return "", fmt.Errorf("failed to get reported devices list, reason: %v", err)
	}
	reportedDeviceSlice, _ := reportedDeviceResp.ReportedDevice()

	if len(reportedDeviceSlice.Slice()) == 0 {
		return "", fmt.Errorf("cannot find NICs for vmId: %s", id)
	}

	var nicRegex = regexp.MustCompile(`^(eth|en|br\-ex).*`)

	for _, reportedDevice := range reportedDeviceSlice.Slice() {
		nicName, _ := reportedDevice.Name()
		if !nicRegex.MatchString(nicName) {
			klog.Infof("ovirt vm id: %s ,  skipped nic %s , naming regex mismatch", id, nicName)
			continue
		}

		ips, hasIps := reportedDevice.Ips()
		if hasIps {
			for _, ip := range ips.Slice() {
				ipAddress, hasAddress := ip.Address()

				if _, ok := excludeAddr[ipAddress]; ok {
					klog.Infof("ipAddress %s is excluded from usable IPs", ipAddress)
					continue
				}

				if hasAddress {
					klog.Infof("ovirt vm id: %s , found usable IP %s", id, ipAddress)
					return ipAddress, nil
				}
			}
		}
	}
	return "", fmt.Errorf("coudlnt find usable IP address for vm id: %s", id)
}

func (is *InstanceService) getAffinityGroups(cID string, agNames []string) (ag []*ovirtsdk.AffinityGroup, err error) {
	var ags []*ovirtsdk.AffinityGroup
	res, err := is.Connection.SystemService().ClustersService().
		ClusterService(cID).AffinityGroupsService().
		List().Send()
	if err != nil {
		return nil, err
	}
	agNamesMap := make(map[string]*ovirtsdk.AffinityGroup)
	for _, af := range res.MustGroups().Slice() {
		agNamesMap[af.MustName()] = af
	}
	for _, agName := range agNames {
		if _, ok := agNamesMap[agName]; !ok {
			return nil, errors.Errorf("affinity group %v was not found on cluster %v", agName, cID)
		}
		ags = append(ags, agNamesMap[agName])
	}
	return ags, nil
}

// handleAffinityGroups adds the VM to the provided affinity groups
func (is *InstanceService) handleAffinityGroups(vm *ovirtsdk.Vm, cID string, agsName []string) error {
	ags, err := is.getAffinityGroups(cID, agsName)
	if err != nil {
		return err
	}
	agService := is.Connection.SystemService().ClustersService().
		ClusterService(cID).AffinityGroupsService()
	for _, ag := range ags {
		klog.Infof("Adding machine %v to affinity group %v", vm.MustName(), ag.MustName())
		_, err = agService.GroupService(ag.MustId()).VmsService().Add().Vm(vm).Send()

		// TODO: bug 1932320: Remove error handling workaround when BZ#1931932 is resolved and backported
		if err != nil && !errors.Is(err, ovirtsdk.XMLTagNotMatchError{ActualTag: "action", ExpectedTag: "vm"}) {
			return errors.Errorf(
				"failed to add VM %s to AffinityGroup %s, error: %v",
				vm.MustName(),
				ag.MustName(),
				err)
		}
	}
	return nil
}

// getHostsInCluster will return a HostSlice of the hosts in a given cluster ID.
func (is *InstanceService) getHostsInCluster(clusterId string) (*ovirtsdk.HostSlice, error) {
	clusterService := is.Connection.SystemService().ClustersService().ClusterService(clusterId)
	clusterGet, err := clusterService.Get().Send()
	if err != nil {
		return nil, errors.Errorf("failed to get the cluster: %v", err)
	}
	clusterName := clusterGet.MustCluster().MustName()
	hostsInCluster, err := is.Connection.SystemService().HostsService().List().Search(
		fmt.Sprintf("cluster=%s", clusterName)).Send()
	if err != nil {
		return nil, errors.Errorf("failed to get the list of hosts in the cluster: %v", err)
	}
	return hostsInCluster.MustHosts(), nil
}

// handleAutoPinning updates the VM after creation to set the auto pinning policy configuration.
func (is *InstanceService) handleAutoPinning(id string, autoPinningPolicy ovirtsdk.AutoPinningPolicy) error {
	vmService := is.Connection.SystemService().VmsService().VmService(id)
	optimizeCpuSettings := autoPinningPolicy == ovirtsdk.AUTOPINNINGPOLICY_ADJUST
	_, err := vmService.AutoPinCpuAndNumaNodes().OptimizeCpuSettings(optimizeCpuSettings).Send()
	if err != nil {
		return errors.Errorf("failed to set the auto pinning policy on the VM!, %v", err)
	}
	return nil
}

func mapAutoPinningPolicy(policy string) ovirtsdk.AutoPinningPolicy {
	switch policy {
	case "none":
		return ovirtsdk.AUTOPINNINGPOLICY_DISABLED
	case "resize_and_pin":
		return ovirtsdk.AUTOPINNINGPOLICY_ADJUST
	default:
		return ovirtsdk.AUTOPINNINGPOLICY_DISABLED
	}
}
