package auth

import (
	"bytes"
	"context"
	"os"
	"path/filepath"

	"github.com/docker/cli/cli/config/configfile"
	"github.com/docker/cli/cli/config/types"
	"github.com/docker/docker/pkg/homedir"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	kubeConfigFileDir    = ".kube"
	kubeConfigFileName   = "config"
	dockerconfigSelector = "type=" + string(corev1.SecretTypeDockerConfigJson)
)

var (
	kubeConfigPath string
)

// FromKubeSecretDockerConfig finds auth for a given host in kubernetes kubernetes.io/dockerconfigjson secret.
func FromKubeSecretDockerConfig(host string) *PassKeyChain {
	clientset, err := createKubeClient()
	if err != nil {
		logrus.WithError(err).Infof("failed to create the kube clientset")
		return nil
	}

	secrets, err := clientset.CoreV1().Secrets(metav1.NamespaceAll).List(context.Background(), metav1.ListOptions{FieldSelector: dockerconfigSelector})
	if err != nil {
		logrus.WithError(err).Infof("failed to list all secrets")
		return nil
	}

	dockerConfigs := make([]configfile.ConfigFile, 0)
	for _, secret := range secrets.Items {
		dockerConfig := configfile.ConfigFile{}
		err = dockerConfig.LoadFromReader(bytes.NewReader(secret.Data[corev1.DockerConfigJsonKey]))
		if err != nil {
			logrus.WithError(err).Infof("failed to unmarshal secret data")
			return nil
		}
		dockerConfigs = append(dockerConfigs, dockerConfig)
	}
	var authConfig types.AuthConfig
	for _, dockerConfig := range dockerConfigs {
		// Find the auth for the host.
		authConfig, err = dockerConfig.GetAuthConfig(host)
		if err != nil {
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

// setKubeConfigPath gets the kubeConfigFilePath.
func setKubeConfigFilePath() {
	if kubeConfigPath != "" {
		return
	}
	kubeConfigPath = os.Getenv("KUBECONFIG")
	if kubeConfigPath == "" {
		kubeConfigPath = filepath.Join(homedir.Get(), kubeConfigFileDir, kubeConfigFileName)
	}
}

// 	getKubeConfig get the KubeConfig of Kubenetes cluster.
func getKubeConfig() (*rest.Config, error) {
	var err error
	var config *rest.Config
	if kubeConfigPath == "" {
		setKubeConfigFilePath()
	}
	if config, err = rest.InClusterConfig(); err != nil {
		if config, err = clientcmd.BuildConfigFromFlags("", kubeConfigPath); err != nil {
			return nil, err
		}
	}
	return config, nil
}

// getKubeClient creates a kubernetes client.
func createKubeClient() (*kubernetes.Clientset, error) {
	var err error
	kubeConfig, err := getKubeConfig()
	if err != nil {
		return nil, err
	}
	// creates the clientset
	return kubernetes.NewForConfig(kubeConfig)
}
