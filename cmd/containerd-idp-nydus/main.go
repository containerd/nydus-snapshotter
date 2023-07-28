package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/sirupsen/logrus"
)

const nydusSnapshotterAuthEndpoint = "/api/v1/remote/auth"
const defaultNydusSystemControllerAddress = "/var/run/containerd-nydus/system.sock"

type ImagePullCreds struct {
	Host   string `json:"host"`
	User   string `json:"user"`
	Secret string `json:"secret"`
}

func buildTransport(sock string) http.RoundTripper {
	return &http.Transport{
		MaxIdleConns:          10,
		IdleConnTimeout:       10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			dialer := &net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 5 * time.Second,
			}
			return dialer.DialContext(ctx, "unix", sock)
		},
	}
}

func NewClient(sock string) http.Client {
	transport := buildTransport(sock)
	return http.Client{
		Timeout:   20 * time.Second,
		Transport: transport,
	}
}

func main() {
	logF, err := os.OpenFile("/var/log/containerd-idp-nydus.log", os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return
	}

	logrus.SetOutput(logF)

	buf := make([]byte, 4096)
	_, err = os.Stdin.Read(buf)
	if err != nil {
		logrus.Fatalf("Receive credential error, %v", err)
	}

	client := NewClient(defaultNydusSystemControllerAddress)

	body := bytes.NewBuffer(buf)
	url := "http://unix" + nydusSnapshotterAuthEndpoint
	req, err := http.NewRequest(http.MethodPut, url, body)
	if err != nil {
		logrus.Fatalf("new request error, %v", err)
	}

	req.Header.Add("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		logrus.Fatalf("Send credentials error, %v", err)
	}

	defer resp.Body.Close()

	return
}
