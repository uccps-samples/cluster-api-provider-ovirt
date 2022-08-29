/*
Copyright oVirt Authors
SPDX-License-Identifier: Apache-2.0
*/

package ovirt

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	apicorev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Creds struct {
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

//TODO: remove CAFILE and use CABundle on CreateAPIConnection

// getCredentialsSecret fetches the secret in the given namespace which contains the oVirt engine credentials
func getCredentialsSecret(coreClient client.Client, namespace string, secretName string) (*Creds, error) {
	var credentialsSecret apicorev1.Secret
	key := client.ObjectKey{Namespace: namespace, Name: secretName}

	if err := coreClient.Get(context.Background(), key, &credentialsSecret); err != nil {
		if errors.IsNotFound(err) {
			return nil, fmt.Errorf(
				"error credentials secret %s not found in namespace %s: %w", secretName, namespace, err)
		}
		return nil, fmt.Errorf(
			"error getting credentials secret %s in namespace %s: %w", secretName, namespace, err)
	}

	o := Creds{}
	o.URL = string(credentialsSecret.Data[secretFieldUrl])
	o.Username = string(credentialsSecret.Data[secretFieldUsername])
	o.Password = string(credentialsSecret.Data[secretFieldPassword])
	o.CAFile = string(credentialsSecret.Data[secretFieldCafile])
	insecure, err := strconv.ParseBool(string(credentialsSecret.Data[secretFieldInsecure]))
	if err != nil {
		return nil, fmt.Errorf("failed to identify %s in credentials %w", secretFieldInsecure, err)
	}
	o.Insecure = insecure
	o.CABundle = string(credentialsSecret.Data[secretFieldCaBundle])

	// write CA bundle to a file if exist.
	// its best if we could mount the secret into a file,
	// but this controller deployment cannot
	if o.CABundle != "" {
		caFilePath, err := writeCA(strings.NewReader(o.CABundle))
		if err != nil {
			klog.Errorf("failed to extract and store the CA %s", err)
			return nil, err
		}
		o.CAFile = caFilePath
	}
	return &o, nil
}

// TODO: remove after we port to CABundle
func writeCA(source io.Reader) (string, error) {
	f, err := os.CreateTemp("", "ovirt-ca-bundle")
	if err != nil {
		return "", err
	}
	defer f.Close()
	content, err := io.ReadAll(source)
	if err != nil {
		return "", err
	}
	_, err = f.Write(content)
	if err != nil {
		return "", err
	}
	return f.Name(), nil
}
