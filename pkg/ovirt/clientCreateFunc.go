package ovirt

import (
	"fmt"

	ovirtclient "github.com/ovirt/go-ovirt-client/v2"
)

type CreateOVirtClientFunc func(creds *Credentials, logger *KLogr) (ovirtclient.Client, error)

var CreateNewOVirtClient = func(creds *Credentials, logger *KLogr) (ovirtclient.Client, error) {
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
		logger,
		nil,
		nil, // no verify, will be done when getting cached client
	)
}
