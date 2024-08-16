package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/syslog"
	"os"

	"github.com/containerd/nri/skel"
	types "github.com/containerd/nri/types/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	rootfspersister "github.com/containerd/nydus-snapshotter/pkg/rootfs_persister"
)

const pluginName = "rootfsPersisterNRI"

const (
	ContainerdAddress         = "/run/containerd/containerd.sock"
	CRIRuntimeServiceEndpoint = "unix://" + ContainerdAddress
)

var (
	log       *logrus.Logger
	logWriter *syslog.Writer
)

func SetupSyslogLogger() {
	log = logrus.StandardLogger()
	log.SetFormatter(&logrus.TextFormatter{
		PadLevelText: true,
	})
	var err error
	logWriter, err = syslog.New(syslog.LOG_INFO, pluginName)
	if err == nil {
		log.SetOutput(logWriter)
	} else {
		log.Fatalf("Failed to connect to syslog")
	}
}

type rootfsPersister struct{}

func (c *rootfsPersister) Type() string {
	return "rootfs-persister"
}

type Config struct {
	Pid int `json:"pid"`
}

func (c *rootfsPersister) Invoke(ctx context.Context, r *types.Request) (*types.Result, error) {
	result := r.NewResult(pluginName)

	var config Config
	if err := json.Unmarshal(r.Conf, &config); err != nil {
		return result, errors.Wrapf(err, "unmarshal NRI config")
	}

	if r.State != "postCreate" && r.State != "removal" {
		return result, nil
	}

	containerID := r.ID
	if containerID == "" {
		return nil, errors.Errorf("container id is empty")
	}

	log.Infof("Handling event %q for container %s", r.State, containerID)

	ctx = rootfspersister.WithLogger(ctx, log)

	switch {
	case r.State == "postCreate":
		if err := rootfspersister.HandlePostCreationEvent(ctx, config.Pid, ContainerdAddress, CRIRuntimeServiceEndpoint, containerID); err != nil {
			return nil, errors.Wrapf(err, "handle post-create event for container %s", containerID)
		}
	case r.State == "removal":
		if err := rootfspersister.HandleRemovalEvent(ctx, CRIRuntimeServiceEndpoint, containerID); err != nil {
			return nil, errors.Wrapf(err, "handle removal event for container %s", containerID)
		}
	default:
		log.Infof("Event %s won't be handled. containerID=%s", r.State, containerID)
		return result, nil
	}

	return result, nil
}

func main() {
	log = logrus.StandardLogger()

	var pvcName string
	var pvcNamespace string

	locateCmd := &cobra.Command{
		Use:   "locate",
		Short: "Locate the EBS block device's path by its PVC",
		RunE: func(_ *cobra.Command, _ []string) error {
			ctx := context.Background()
			ctx = rootfspersister.WithLogger(ctx, log)

			devPath, err := rootfspersister.GetBlockDevicePathFromPVC(ctx, pvcNamespace, pvcName)
			if err != nil {
				log.Errorf("Failed to get block device path for %s/%s: %v", pvcNamespace, pvcName, err)
				os.Exit(1)
			}

			log.Infof("device path %s", devPath)

			fmt.Print(devPath)
			return nil
		},
	}

	invokeCmd := &cobra.Command{
		Use:   "invoke",
		Short: "It is usually called by the Containerd to handle different NRI hook events",
		RunE: func(_ *cobra.Command, _ []string) error {
			SetupSyslogLogger()
			ctx := context.Background()
			if err := skel.Run(ctx, &rootfsPersister{}); err != nil {
				log.Errorf("Failed to run rootfs persister: %v", err)
				os.Exit(1)
			}
			return nil
		},
	}

	rootCmd := &cobra.Command{
		Use:     "rootfs-persister",
		Short:   "A NRI plugin that interacts with nydus-snapshotter to redirect the active snapshot's local path to a dedicated disk which is backing by EBS",
		Version: "null",
		RunE: func(_ *cobra.Command, _ []string) error {
			// Do nothing, don't print help message since it pollutes stdout
			return nil
		},
	}

	rootCmd.AddCommand(locateCmd)
	rootCmd.AddCommand(invokeCmd)

	locateCmd.Flags().StringVar(&pvcName, "pvc-name", "", "PVC name")
	locateCmd.Flags().StringVar(&pvcNamespace, "pvc-namespace", "", "PVC namespace")

	if err := rootCmd.Execute(); err != nil {
		log.Errorf("Failed to execute: %v", err)
		os.Exit(1)
	}
}
