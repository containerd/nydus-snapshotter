package main

import (
	"os"

	"github.com/containerd/stargz-snapshotter/analyzer/fanotify/service"
	"github.com/spf13/cobra"
)

func newFanotifyCommand() *cobra.Command {
	var execCommand = &cobra.Command{
		Use:   "fanotify TARGET",
		Args:  cobra.MinimumNArgs(1),
		Short: "Run fanotify server",
		Run: func(cmd *cobra.Command, args []string) {
			if target := args[0]; target != "" {
				service.Serve(target, os.Stdin, os.Stdout)
			}
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		Hidden:        true,
	}
	return execCommand
}
