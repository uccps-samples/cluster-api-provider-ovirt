package machine

import (
	"fmt"

	ovirtconfigv1 "github.com/openshift/cluster-api-provider-ovirt/pkg/apis/ovirtprovider/v1beta1"
	ovirtC "github.com/ovirt/go-ovirt-client/v2"
	"github.com/pkg/errors"
)

const (
	ErrorInvalidMachineObject = "error validating machine object fields"
	noHugePages               = 0
	hugePages2M               = 2048
	hugePages1GB              = 1048576
)

// validateMachine validates the machine object yaml fields and
// returns InvalidMachineConfiguration in case the validation failed
func validateMachine(ovirtClient ovirtC.Client, config *ovirtconfigv1.OvirtMachineProviderSpec) error {
	// UserDataSecret
	if config.UserDataSecret == nil {
		return fmt.Errorf("%s UserDataSecret must be provided!", ErrorInvalidMachineObject)
	} else if config.UserDataSecret.Name == "" {
		return fmt.Errorf("%s UserDataSecret *Name* must be provided!", ErrorInvalidMachineObject)
	}

	err := validateInstanceID(config)
	if err != nil {
		return fmt.Errorf("error validating InstanceID %v", err)
	}

	// root disk of the node
	if config.OSDisk == nil {
		return fmt.Errorf("%s OS Disk (os_disk) must be specified!", ErrorInvalidMachineObject)
	} else if config.OSDisk.SizeGB == 0 {
		return fmt.Errorf("%s OS Disk (os_disk) *SizeGB* must be specified!", ErrorInvalidMachineObject)
	}

	err = validateVirtualMachineType(config.VMType)
	if err != nil {
		return fmt.Errorf("error validating Machine Type %w", err)
	}

	if config.AutoPinningPolicy != "" {
		supported, err := ovirtClient.SupportsFeature(ovirtC.FeatureAutoPinning)
		if err != nil {
			return errors.Wrap(err, "failed to check autopinning support")
		}
		if !supported {
			return errors.Wrap(err, "autopinning is not supported.")
		}
	}
	if err := validateHugepages(config.Hugepages); err != nil {
		return errors.Wrap(err, "error validating Hugepages")
	}

	if err := validateGuaranteedMemory(config); err != nil {
		return errors.Wrap(err, "error validating GuaranteedMemory")
	}

	return nil
}

// validateInstanceID execute validations regarding the InstanceID.
// Returns: nil or InvalidMachineConfiguration
func validateInstanceID(config *ovirtconfigv1.OvirtMachineProviderSpec) error {
	// Cannot set InstanceTypeID and at same time: MemoryMB OR CPU
	if len(config.InstanceTypeId) != 0 {
		if config.MemoryMB != 0 || config.CPU != nil {
			return fmt.Errorf(
				"%s InstanceTypeID and MemoryMB OR CPU cannot be set at the same time", ErrorInvalidMachineObject)
		}
	} else {
		if config.MemoryMB == 0 {
			return fmt.Errorf("%s MemoryMB must be specified", ErrorInvalidMachineObject)
		}
		if config.CPU == nil {
			return fmt.Errorf("%s CPU must be specified", ErrorInvalidMachineObject)
		}
	}
	return nil
}

// validateVirtualMachineType execute validations regarding the
// Virtual Machine type (desktop, server, high_performance).
// Returns: nil or InvalidMachineConfiguration
func validateVirtualMachineType(vmtype string) error {
	if len(vmtype) == 0 {
		return fmt.Errorf("VMType (keyword: type in YAML) must be specified")
	}
	switch vmtype {
	case "server", "high_performance", "desktop":
		return nil
	default:
		return fmt.Errorf(
			"error creating oVirt instance: The machine type must "+
				"be one of the following options: "+
				"server, high_performance or desktop. The value: %s is not valid", vmtype)
	}
}

// validateHugepages execute validation regarding the Virtual Machine hugepages
// custom property (2048, 1048576).
// Returns: nil or error
func validateHugepages(value int32) error {
	switch value {
	case noHugePages, hugePages2M, hugePages1GB:
		return nil
	default:
		return fmt.Errorf(
			"error creating oVirt instance: The machine `hugepages` custom property must "+
				"be one of the following options: 2048, 1048576. "+
				"The value: %d is not valid", value)
	}
}

// validateGuaranteedMemory execute validation regarding the Virtual Machine validateGuaranteedMemory
// Returns: nil or error
func validateGuaranteedMemory(config *ovirtconfigv1.OvirtMachineProviderSpec) error {
	if config.MemoryMB == 0 {
		return fmt.Errorf("MemoryMB must be specified")
	}
	if config.GuaranteedMemoryMB > config.MemoryMB {
		return fmt.Errorf("GuaranteedMemoryMB (%d) cannot be bigger then  MemoryMB (%d)",
			config.GuaranteedMemoryMB,
			config.MemoryMB)
	}

	return nil

}
