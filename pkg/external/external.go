package external

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/containerd/nydus-snapshotter/pkg/external/backend"
	"github.com/pkg/errors"
)

type Options struct {
	Dir              string
	Handler          backend.Handler
	MetaOutput       string
	BackendOutput    string
	AttributesOutput string
}

type Attribute struct {
	Pattern string
}

// Handle handles the directory and generates the backend meta and attributes.
func Handle(ctx context.Context, opts Options) error {
	walker := backend.NewWalker()

	backendRet, err := walker.Walk(ctx, opts.Dir, opts.Handler)
	if err != nil {
		return err
	}
	generators, err := NewGenerators(*backendRet)
	if err != nil {
		return err
	}
	ret, err := generators.Generate()
	if err != nil {
		return err
	}
	bkd := ret.Backend
	attributes := []Attribute{}
	for _, file := range ret.Files {
		attributes = append(attributes, Attribute{
			Pattern: file,
		})
	}

	if err := os.WriteFile(opts.MetaOutput, ret.Meta, 0644); err != nil {
		return errors.Wrapf(err, "write meta to %s", opts.MetaOutput)
	}

	backendBytes, err := json.MarshalIndent(bkd, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(opts.BackendOutput, backendBytes, 0644); err != nil {
		return errors.Wrapf(err, "write backend json to %s", opts.BackendOutput)
	}

	attributeContent := []string{}
	for _, attribute := range attributes {
		attributeContent = append(attributeContent, fmt.Sprintf("%s\ttype=external", attribute.Pattern))
	}
	if err := os.WriteFile(opts.AttributesOutput, []byte(strings.Join(attributeContent, "\n")), 0644); err != nil {
		return errors.Wrapf(err, "write attributes to %s", opts.BackendOutput)
	}

	return nil
}
