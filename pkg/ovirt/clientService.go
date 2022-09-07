package ovirt

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/go-logr/logr"
	k8sCorev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2/klogr"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type ClientService interface {
	Run(context.Context)
	Shutdown(timeout time.Duration)

	NewCachedClient(name string) CachedOVirtClient
	AddListener(CredentialUpdatable) ClientService
	AddListeners(updatables ...CredentialUpdatable) ClientService
}

type clientService struct {
	logger logr.Logger

	secretInformer       cache.SharedIndexInformer
	credentialUpdateChan chan interface{}

	wg *sync.WaitGroup

	credUpdateListener []CredentialUpdatable
}

type SecretsToWatch struct {
	Namespace  string
	SecretName string
}

func NewClientService(
	cfg *rest.Config,
	watchedCreds SecretsToWatch,
) *clientService {
	kubeClientSet := kubernetes.NewForConfigOrDie(rest.AddUserAgent(cfg, "ovirt-client-service"))
	informersForNamespace := informers.NewSharedInformerFactoryWithOptions(
		kubeClientSet,
		10*time.Minute,
		informers.WithNamespace(watchedCreds.Namespace),
		informers.WithTweakListOptions(func(lo *v1.ListOptions) {
			lo.FieldSelector = fmt.Sprintf("metadata.name=%s", watchedCreds.SecretName)
		}),
	)

	log.SetLogger(klogr.New())
	service := &clientService{
		logger: log.Log.WithName("ovirt-client-service").V(3),

		secretInformer:       informersForNamespace.Core().V1().Secrets().Informer(),
		credentialUpdateChan: make(chan interface{}),

		// allocate 4 updatables:
		// node and providerId controller, actuator and healthz
		credUpdateListener: make([]CredentialUpdatable, 0, 4),
		wg:                 &sync.WaitGroup{},
	}

	service.secretInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			service.credentialUpdateChan <- obj
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			service.credentialUpdateChan <- newObj
		},
	})

	return service
}

func (service *clientService) NewCachedClient(name string) CachedOVirtClient {
	newClient := NewCachedOVirtClient(name)
	service.AddListener(newClient)
	return newClient
}

func (service *clientService) AddListener(updatable CredentialUpdatable) ClientService {
	service.credUpdateListener = append(service.credUpdateListener, updatable)
	return service
}

func (service *clientService) AddListeners(updatables ...CredentialUpdatable) ClientService {
	for _, updatable := range updatables {
		service.AddListener(updatable)
	}
	return service
}

func (service *clientService) Run(ctx context.Context) {
	defer utilruntime.HandleCrash()

	service.logger.Info("Starting credential update service")

	service.wg.Add(1)
	go func() {
		defer service.wg.Done()

		service.secretInformer.Run(ctx.Done())
	}()

	if !cache.WaitForCacheSync(ctx.Done(), service.secretInformer.HasSynced) {
		service.logger.Error(nil, "timed out waiting for informer caches to sync")
	}
	service.logger.Info("Credential update service synced and ready")

	service.wg.Add(1)
	go service.processCredentialUpdate(ctx, service.wg)
}

func (service *clientService) processCredentialUpdate(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	for {
		select {
		case credObj := <-service.credentialUpdateChan:
			secret, ok := credObj.(*k8sCorev1.Secret)
			if !ok {
				service.logger.Error(nil, "failed to parse k8s secret")
				break
			}

			creds, err := FromK8sSecret(secret)
			if err != nil {
				service.logger.Error(err, "failed to parse k8s secret to oVirt credentials")
				break
			}

			err = writeCA(creds)
			if err != nil {
				service.logger.Error(err, "failed to write CA to temporary file")
				break
			}

			for _, listener := range service.credUpdateListener {
				listener.SetCredentials(creds)
			}

		case <-ctx.Done():
			return
		}
	}
}

func (service *clientService) Shutdown(timeout time.Duration) {
	service.logger.Info("Shutting down oVirt client service...")
	c := make(chan struct{})
	go func() {
		defer close(c)
		service.wg.Wait()
	}()
	select {
	case <-c:
		service.logger.Info("oVirt client service shutdown gracefully...")
	case <-time.After(timeout):
		service.logger.Info(fmt.Sprintf("oVirt client service shutdown timed out at %s", timeout.String()))
	}
}

type CredentialUpdatable interface {
	SetCredentials(*Credentials)
}
