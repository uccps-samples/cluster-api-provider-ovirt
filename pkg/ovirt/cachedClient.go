package ovirt

import (
	"fmt"
	"sync"

	"github.com/go-logr/logr"
	ovirtclient "github.com/ovirt/go-ovirt-client/v2"
	"k8s.io/klog/v2/klogr"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type CachedOVirtClient interface {
	Get() (ovirtclient.Client, error)
	WithCreateFunc(CreateOVirtClientFunc)
}

type cachedOVirtClient struct {
	logger logr.Logger

	credentials      *Credentials
	client           ovirtclient.Client
	updateLock       *sync.Mutex
	clientCreateFunc CreateOVirtClientFunc
}

func NewCachedOVirtClient(name string) *cachedOVirtClient {
	log.SetLogger(klogr.New())

	return &cachedOVirtClient{
		logger: log.Log.WithName(fmt.Sprintf("cached-client-%s", name)).V(3),

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

	cachedClient.logger.Info("Updating cached oVirt client credentials")
	cachedClient.credentials = newCredentials
	cachedClient.buildClient()
}

func (cachedClient *cachedOVirtClient) buildClient() error {
	if cachedClient.clientCreateFunc == nil {
		cachedClient.clientCreateFunc = CreateNewOVirtClient
	}

	newClient, err := cachedClient.clientCreateFunc(cachedClient.credentials)
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
		cachedClient.logger.Info("Building new oVirt client...")
		err := cachedClient.buildClient()
		if err != nil {
			return nil, err
		}
	}

	return cachedClient.client, nil
}
