package auth

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"

	"k8s.io/client-go/tools/clientcmd"

	"github.com/docker/cli/cli/config/configfile"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

var (
	kubeSecretListener *KubeSecretListener
	configMu           sync.Mutex
)

type KubeSecretListener struct {
	dockerConfigs map[string]*configfile.ConfigFile
	informer      cache.SharedIndexInformer
}

func InitKubeSecretListener(ctx context.Context, kubeconfigPath string) error {
	configMu.Lock()
	defer configMu.Unlock()
	if kubeSecretListener != nil {
		return nil
	}
	kubeSecretListener = &KubeSecretListener{
		dockerConfigs: make(map[string]*configfile.ConfigFile),
	}

	if kubeconfigPath != "" {
		_, err := os.Stat(kubeconfigPath)
		if err != nil && !os.IsNotExist(err) {
			logrus.WithError(err).Warningf("kubeconfig does not exist, kubeconfigPath %s", kubeconfigPath)
			return err
		} else if err != nil {
			logrus.WithError(err).Warningf("failed to detect kubeconfig existence, kubeconfigPath %s", kubeconfigPath)
			return err
		}
	}
	loadingRule := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRule.ExplicitPath = kubeconfigPath
	clientConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loadingRule,
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		logrus.WithError(err).Warningf("failed to load kubeconfig")
		return err
	}
	clientset, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		logrus.WithError(err).Warningf("failed to create kubernetes client")
		return err
	}
	if err := kubeSecretListener.SyncKubeSecrets(ctx, clientset); err != nil {
		logrus.WithError(err).Warningf("failed to sync secrets")
		return err
	}

	return nil
}

func (kubelistener *KubeSecretListener) addDockerConfig(key string, obj interface{}) error {
	data, ok := obj.(*corev1.Secret).Data[corev1.DockerConfigJsonKey]
	if !ok {
		return fmt.Errorf("failed to get data from new object")
	}
	dockerConfig := configfile.ConfigFile{}
	if err := dockerConfig.LoadFromReader(bytes.NewReader(data)); err != nil {
		return errors.Wrap(err, "failed to load docker config json from secret")
	}
	configMu.Lock()
	kubelistener.dockerConfigs[key] = &dockerConfig
	configMu.Unlock()
	return nil
}

func (kubelistener *KubeSecretListener) deleteDockerConfig(key string) {
	configMu.Lock()
	delete(kubelistener.dockerConfigs, key)
	configMu.Unlock()
}

func (kubelistener *KubeSecretListener) SyncKubeSecrets(ctx context.Context, clientset *kubernetes.Clientset) error {
	if kubelistener.informer != nil {
		return nil
	}
	informer := cache.NewSharedIndexInformer(
		&cache.ListWatch{
			ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
				options.FieldSelector = "type=" + string(corev1.SecretTypeDockerConfigJson)
				return clientset.CoreV1().Secrets(metav1.NamespaceAll).List(context.Background(), options)
			},
			WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
				options.FieldSelector = "type=" + string(corev1.SecretTypeDockerConfigJson)
				return clientset.CoreV1().Secrets(metav1.NamespaceAll).Watch(context.Background(), options)
			}},
		&corev1.Secret{},
		0,
		cache.Indexers{},
	)
	kubelistener.informer = informer
	_, err := kubelistener.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err != nil {
				logrus.WithError(err).Errorf("failed to get key for secret from cache")
				return
			}
			if err := kubelistener.addDockerConfig(key, obj); err != nil {
				logrus.WithError(err).Errorf("failed to add a new dockerconfigjson")
				return
			}
		},
		UpdateFunc: func(_, newObj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(newObj)
			if err != nil {
				logrus.WithError(err).Errorf("failed to get key for secret from cache")
				return
			}
			if err := kubelistener.addDockerConfig(key, newObj); err != nil {
				logrus.WithError(err).Errorf("failed to add a new dockerconfigjson")
				return
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err != nil {
				logrus.WithError(err).Errorf("failed to get key for secret from cache")
			}
			kubelistener.deleteDockerConfig(key)
		}},
	)
	if err != nil {
		return errors.Wrap(err, "add event handler to informer")
	}
	go kubelistener.informer.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return fmt.Errorf("timed out for syncing cache")
	}
	return nil
}

func (kubelistener *KubeSecretListener) GetCredentialsStore(host string) *PassKeyChain {
	configMu.Lock()
	defer configMu.Unlock()
	for _, dockerConfig := range kubelistener.dockerConfigs {
		// Find the auth for the host.
		authConfig, err := dockerConfig.GetAuthConfig(host)
		if err != nil {
			logrus.WithError(err).Errorf("failed to get auth config for host %s", host)
			continue
		}
		if len(authConfig.Username) != 0 && len(authConfig.Password) != 0 {
			return &PassKeyChain{
				Username: authConfig.Username,
				Password: authConfig.Password,
			}
		}
	}
	return nil
}

func FromKubeSecretDockerConfig(host string) *PassKeyChain {
	if kubeSecretListener != nil {
		return kubeSecretListener.GetCredentialsStore(host)
	}
	return nil
}
