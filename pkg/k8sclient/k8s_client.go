/*
Copyright 2023 Beijing Volcano Engine Technology Ltd.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package k8sclient

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/nydus-snapshotter/version"
	"github.com/pkg/errors"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	rest "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type NodeEventHandler func(watch.EventType, *v1.Node)
type PodEventHandler func(watch.EventType, *v1.Pod)

type KubeClientConfigGetter interface {
	GetConfig() (*rest.Config, error)
}

var CurrentConfigGetter KubeClientConfigGetter

type InClusterConfigGetter struct{}

func BuildKubeConfig() (*rest.Config, error) {
	p := os.Getenv("KUBECONFIG")

	if p == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		p = filepath.Join(home, ".kube", "config")
	}

	return clientcmd.BuildConfigFromFlags("", p)
}

func (b *InClusterConfigGetter) GetConfig() (*rest.Config, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	return config, nil
}

type KubeConfigFileGetter struct{}

func (b *KubeConfigFileGetter) GetConfig() (*rest.Config, error) {
	return BuildKubeConfig()
}

func init() {
	CurrentConfigGetter = &InClusterConfigGetter{}
}

type KubeClient struct {
	client     *kubernetes.Clientset
	restConfig *rest.Config
}

func newKubeClient(config *rest.Config) (*KubeClient, error) {
	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, errors.Wrapf(err, "building Kubernetes clientset")
	}

	return &KubeClient{client: kubeClient, restConfig: config}, nil
}

func NewKubeClient(qps float32, burst int) (*KubeClient, error) {
	config, err := CurrentConfigGetter.GetConfig()
	if err != nil {
		return nil, err
	}

	config.UserAgent = fmt.Sprintf("nydus-snapshotter/%s", version.Version)
	if qps > 0 {
		config.QPS = qps
	}

	if burst > 0 {
		config.Burst = burst
	}

	return newKubeClient(config)
}

func (c *KubeClient) GetClientset() *kubernetes.Clientset {
	return c.client
}

func (c *KubeClient) GetPersistentVolume(ctx context.Context, name string) (*v1.PersistentVolume, error) {
	return c.client.CoreV1().PersistentVolumes().Get(ctx, name, metav1.GetOptions{})
}

func (c *KubeClient) ListPersistentVolume(ctx context.Context, selector string, timeoutSecs int64) (*v1.PersistentVolumeList, error) {
	return c.client.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{LabelSelector: selector, TimeoutSeconds: &timeoutSecs})
}

func (c *KubeClient) ListPersistentVolumeClaim(ctx context.Context, namespace string, selector string, timeoutSecs int64) (*v1.PersistentVolumeClaimList, error) {
	return c.client.CoreV1().PersistentVolumeClaims(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector, TimeoutSeconds: &timeoutSecs})
}

func (c *KubeClient) GetPersistentVolumeClaim(ctx context.Context, namespace, name string) (*v1.PersistentVolumeClaim, error) {
	return c.client.CoreV1().PersistentVolumeClaims(namespace).Get(ctx, name, metav1.GetOptions{})
}
