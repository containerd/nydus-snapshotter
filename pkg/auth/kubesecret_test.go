package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
)

const (
	testDockerConfigJSONFmt = `
{
	"auths": {
		"%s": {
			"username": "%s",
			"password": "%s",
			"email": "%s",
			"auth": "%s"
		}
	}
}
`
	dockerConfigKey = "testKey"
	registryUser    = "dockeruserfoobar"
	registryPass    = "dockerpassfoobar"
	registryEmail   = "test@alibaba.com"
)

func TestGetCredentialsStore(t *testing.T) {
	assert := assert.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Host may not has kubeconfig, so ignore the error and continue the test
	_ = InitKubeSecretListener(ctx, "")
	assert.NotNil(kubeSecretListener)

	var obj interface{} = &corev1.Secret{
		Data: map[string][]byte{
			corev1.DockerConfigJsonKey: []byte(fmt.Sprintf(testDockerConfigJSONFmt, extraHost, registryUser,
				registryPass, registryEmail, base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", registryUser, registryPass))))),
		},
	}
	err := kubeSecretListener.addDockerConfig(dockerConfigKey, obj)
	assert.Nil(err)

	auth := FromKubeSecretDockerConfig(extraHost)
	assert.Equal(auth.Username, registryUser)
	assert.Equal(auth.Password, registryPass)

	auth = kubeSecretListener.GetCredentialsStore(extraHost)
	assert.Equal(auth.Username, registryUser)
	assert.Equal(auth.Password, registryPass)
	_, ok := kubeSecretListener.dockerConfigs[dockerConfigKey]
	assert.Equal(ok, true)
	kubeSecretListener.deleteDockerConfig(dockerConfigKey)
	_, ok = kubeSecretListener.dockerConfigs[dockerConfigKey]
	assert.Equal(ok, false)

	auth = kubeSecretListener.GetCredentialsStore(extraHost)
	assert.Nil(auth)
}
