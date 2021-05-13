package ovirt

import (
	ovirtconfigv1 "github.com/openshift/cluster-api-provider-ovirt/pkg/apis/ovirtprovider/v1beta1"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	ovirtsdk "github.com/ovirt/go-ovirt"
)

type OvirtClient interface {
	CreateVmByMachine(machine *machinev1.Machine, ignition []byte, providerSpec *ovirtconfigv1.OvirtMachineProviderSpec) (instance *Instance, err error)
	DeleteVM(id string) error
	GetVmByMachine(machine machinev1.Machine) (instance *Instance, err error)
	GetVmByID(resourceId string) (instance *Instance, err error)
	GetVmByName(mName string) (*Instance, error)
	StartVM(id string) error
	FindVirtualMachineIP(id string, excludeAddr map[string]int) (string, error)
	GetEngineVersion() (*ovirtsdk.Version, error)
}
