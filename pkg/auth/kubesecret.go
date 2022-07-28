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

var kubeSecretListener *KubeSecretListener

type KubeSecretListener struct {
	dockerConfigs map[string]*configfile.ConfigFile
	configMu      sync.Mutex
	informer      cache.SharedIndexInformer
}

func InitKubeSecretListener(ctx context.Context, kubeconfigPath string) {
	if kubeSecretListener != nil {
		return
	}
	kubeSecretListener = &KubeSecretListener{
		dockerConfigs: make(map[string]*configfile.ConfigFile),
	}
	go func() {
		if kubeconfigPath != "" {
			_, err := os.Stat(kubeconfigPath)
			if err != nil && !os.IsNotExist(err) {
				logrus.WithError(err).Infof("kubeconfig does not exist, kubeconfigPath %s", kubeconfigPath)
				return
			} else if err != nil {
				logrus.WithError(err).Infof("failed to detect kubeconfig existence, kubeconfigPath %s", kubeconfigPath)
				return
			}
		}
		loadingRule := clientcmd.NewDefaultClientConfigLoadingRules()
		loadingRule.ExplicitPath = kubeconfigPath
		clientConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
			loadingRule,
			&clientcmd.ConfigOverrides{},
		).ClientConfig()
		if err != nil {
			logrus.WithError(err).Infof("failed to load kubeconfig")
			return
		}
		clientset, err := kubernetes.NewForConfig(clientConfig)
		if err != nil {
			logrus.WithError(err).Infof("failed to create kubernetes client")
			return
		}
		if err := kubeSecretListener.SyncKubeSecrets(ctx, clientset); err != nil {
			logrus.WithError(err).Infof("failed to sync secrets")
			return
		}
	}()
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
	kubelistener.configMu.Lock()
	kubelistener.dockerConfigs[key] = &dockerConfig
	kubelistener.configMu.Unlock()
	return nil
}

func (kubelistener *KubeSecretListener) deleteDockerConfig(key string) error {
	kubelistener.configMu.Lock()
	delete(kubelistener.dockerConfigs, key)
	kubelistener.configMu.Unlock()
	_, exists := kubelistener.dockerConfigs[key]
	if exists {
		return fmt.Errorf("failed to delete docker config")
	}
	return nil
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
	kubelistener.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err != nil {
				logrus.WithError(err).Infof("failed to get key for secret from cache")
				return
			}
			if err := kubelistener.addDockerConfig(key, obj); err != nil {
				logrus.WithError(err).Errorf("failed to add a new dockerconfigjson")
				return
			}
		},
		UpdateFunc: func(old, new interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(new)
			if err != nil {
				logrus.WithError(err).Infof("failed to get key for secret from cache")
				return
			}
			if err := kubelistener.addDockerConfig(key, new); err != nil {
				logrus.WithError(err).Errorf("failed to add a new dockerconfigjson")
				return
			}
		},
		DeleteFunc: func(obj interface{}) {
			key, err := cache.MetaNamespaceKeyFunc(obj)
			if err != nil {
				logrus.WithError(err).Infof("failed to get key for secret from cache")
			}
			if err := kubelistener.deleteDockerConfig(key); err != nil {
				logrus.WithError(err).Errorf("failed to delete docker config")
				return
			}
		}},
	)
	go kubelistener.informer.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), informer.HasSynced) {
		return fmt.Errorf("timed out for syncing cache")
	}
	return nil
}

func (kubelistener *KubeSecretListener) GetCredentialsStore(host string) *PassKeyChain {
	kubelistener.configMu.Lock()
	defer kubelistener.configMu.Unlock()
	for _, dockerConfig := range kubelistener.dockerConfigs {
		// Find the auth for the host.
		authConfig, err := dockerConfig.GetAuthConfig(host)
		if err != nil {
			logrus.WithError(err).Infof("failed to get auth config for host %s", host)
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
