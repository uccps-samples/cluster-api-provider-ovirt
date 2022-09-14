package ovirt

import (
	"fmt"
	"sync"

	ovirtclient "github.com/ovirt/go-ovirt-client/v2"
)

type CachedOVirtClient interface {
	Get() (ovirtclient.Client, error)
	WithCreateFunc(CreateOVirtClientFunc)
}

type cachedOVirtClient struct {
	logger *KLogr
	name   string

	credentials      *Credentials
	client           ovirtclient.Client
	updateLock       *sync.Mutex
	clientCreateFunc CreateOVirtClientFunc
}

func NewCachedOVirtClient(name string) *cachedOVirtClient {
	return &cachedOVirtClient{
		logger: NewKLogr("cached-client", name).WithVInfo(0),
		name:   name,

		credentials:      nil,
		client:           nil,
		updateLock:       &sync.Mutex{},
		clientCreateFunc: nil,
	}
}

func (cachedClient *cachedOVirtClient) WithCreateFunc(createFunc CreateOVirtClientFunc) {
	cachedClient.clientCreateFunc = createFunc
}

func (cachedClient *cachedOVirtClient) SetCredentials(newCredentials *Credentials) {
	cachedClient.updateLock.Lock()
	defer cachedClient.updateLock.Unlock()

	cachedClient.logger.Infof("Updating cached oVirt client credentials")
	cachedClient.credentials = newCredentials
	cachedClient.buildClient()
}

func (cachedClient *cachedOVirtClient) buildClient() error {
	if cachedClient.clientCreateFunc == nil {
		cachedClient.clientCreateFunc = CreateNewOVirtClient
	}

	newClient, err := cachedClient.clientCreateFunc(cachedClient.credentials, NewKLogr("cached-client", cachedClient.name, "ovirt"))
	if err != nil {
		cachedClient.client = nil // invalidate current client, will retrigger build
		return fmt.Errorf("failed to create oVirt client: %v", err)
	}
	cachedClient.client = newClient
	return nil
}

func (cachedClient *cachedOVirtClient) Get() (ovirtclient.Client, error) {
	cachedClient.updateLock.Lock()
	defer cachedClient.updateLock.Unlock()

	if cachedClient.client == nil || cachedClient.client.Test() != nil {
		cachedClient.logger.Infof("Building new oVirt client...")
		err := cachedClient.buildClient()
		if err != nil {
			return nil, err
		}
	}

	return cachedClient.client, nil
}
