/*
 * Copyright (c) 2021. Alibaba Cloud. All rights reserved.
 * Copyright (c) 2022. Nydus Developers. All rights reserved.
 *
 * SPDX-License-Identifier: Apache-2.0
 */

package exporter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/containerd/nydus-snapshotter/pkg/metrics/registry"
	"github.com/pkg/errors"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
)

type Opt func(*FileExporter) error

type FileExporter struct {
	outputFile string
}

var GlobalFileExporter *FileExporter

func init() {
	var exp FileExporter
	GlobalFileExporter = &exp
}

func WithOutputFile(metricsFile string) Opt {
	return func(e *FileExporter) error {
		if metricsFile == "" {
			return errors.New("metrics file path is empty")
		}

		if _, err := os.Create(metricsFile); err != nil {
			return errors.Wrapf(err, "failed to create metrics file: %s", metricsFile)
		}
		e.outputFile = metricsFile
		return nil
	}
}

func NewFileExporter(opts ...Opt) error {
	for _, o := range opts {
		if err := o(GlobalFileExporter); err != nil {
			return err
		}
	}

	return nil
}

func (e *FileExporter) Export() error {
	ms, err := registry.Registry.Gather()
	if err != nil {
		return errors.Wrap(err, "failed to gather all prometheus exporters")
	}
	for _, m := range ms {
		if err := e.exportText(m); err != nil {
			return errors.Wrapf(err, "failed to export text metrics")
		}
	}

	return nil
}

func (e *FileExporter) exportText(m *dto.MetricFamily) error {
	var b bytes.Buffer

	enc := expfmt.NewEncoder(&b, expfmt.FmtText)
	if err := enc.Encode(m); err != nil {
		return errors.Wrapf(err, "failed to encode metrics for %v", m)
	}

	data := map[string]string{
		"time":    time.Now().Format(time.RFC3339),
		"metrics": (&b).String(),
	}
	json, err := json.Marshal(data)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal data for %v", data)
	}
	return e.writeToFile(string(json))
}

func (e *FileExporter) writeToFile(data string) error {
	f, err := os.OpenFile(e.outputFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return errors.Wrapf(err, "failed to open metrics file on %s", e.outputFile)
	}
	defer f.Close()

	if _, err := f.WriteString(fmt.Sprintf("%s\n", data)); err != nil {
		return err
	}

	return nil
}
