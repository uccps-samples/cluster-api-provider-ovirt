package ovirt

import (
	"fmt"

	kloglogger "github.com/ovirt/go-ovirt-client-log-klog/v2"
	ovirtclient "github.com/ovirt/go-ovirt-client/v2"
)

type CreateOVirtClientFunc func(creds *Credentials) (ovirtclient.Client, error)

var CreateNewOVirtClient = func(creds *Credentials) (ovirtclient.Client, error) {
	if creds == nil {
		return nil, fmt.Errorf("credentials are emtpy")
	}

	tls := ovirtclient.TLS()
	if creds.Insecure {
		tls.Insecure()
	} else {
		if creds.CAFile != "" {
			tls.CACertsFromFile(creds.CAFile)
		}
		if creds.CABundle != "" {
			tls.CACertsFromMemory([]byte(creds.CABundle))
		}
		tls.CACertsFromSystem()
	}

	return ovirtclient.NewWithVerify(
		creds.URL,
		creds.Username,
		creds.Password,
		tls,
		kloglogger.New(),
		nil,
		nil,
	)
}
