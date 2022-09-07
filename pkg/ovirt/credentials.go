/*
Copyright oVirt Authors
SPDX-License-Identifier: Apache-2.0
*/

package ovirt

import (
	"fmt"
	"strconv"

	apicorev1 "k8s.io/api/core/v1"
)

type Credentials struct {
	URL      string
	Username string
	Password string
	CAFile   string
	Insecure bool
	CABundle string
}

const (
	secretFieldUrl      = "ovirt_url"
	secretFieldUsername = "ovirt_username"
	secretFieldPassword = "ovirt_password"
	secretFieldCafile   = "ovirt_cafile"
	secretFieldInsecure = "ovirt_insecure"
	secretFieldCaBundle = "ovirt_ca_bundle"
)

func FromK8sSecret(secret *apicorev1.Secret) (*Credentials, error) {
	o := Credentials{}
	o.URL = string(secret.Data[secretFieldUrl])
	o.Username = string(secret.Data[secretFieldUsername])
	o.Password = string(secret.Data[secretFieldPassword])
	o.CAFile = string(secret.Data[secretFieldCafile])
	insecure, err := strconv.ParseBool(string(secret.Data[secretFieldInsecure]))
	if err != nil {
		return nil, fmt.Errorf("failed to identify %s in credentials %w", secretFieldInsecure, err)
	}
	o.Insecure = insecure
	o.CABundle = string(secret.Data[secretFieldCaBundle])
	return &o, nil
}
