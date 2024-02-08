package local

import (
	"context"

	"github.com/containerd/nydus-snapshotter/pkg/external/backend"
)

type object struct {
	Path string `msgpack:"p"`
}

type chunk struct {
	objectID      uint32
	objectContent object
	objectOffset  uint64
}

func (c *chunk) ObjectID() uint32 {
	return c.objectID
}

func (c *chunk) ObjectContent() interface{} {
	return c.objectContent
}

func (c *chunk) ObjectOffset() uint64 {
	return c.objectOffset
}

type Handler struct {
	root     string
	objectID uint32
}

func NewHandler(root string) *Handler {
	return &Handler{
		root:     root,
		objectID: 0,
	}
}

func (handler *Handler) Handle(_ context.Context, file backend.File) ([]backend.Chunk, error) {
	chunks := []backend.Chunk{}
	objectOffsets := backend.SplitObjectOffsets(file.Size, backend.DefaultFileChunkSize)

	for _, objectOffset := range objectOffsets {
		chunks = append(chunks, &chunk{
			objectID: handler.objectID,
			objectContent: object{
				Path: file.RelativePath,
			},
			objectOffset: objectOffset,
		})
	}
	handler.objectID++

	return chunks, nil
}

func (handler *Handler) Backend(_ context.Context) (*backend.Backend, error) {
	return &backend.Backend{
		Type: "local",
		Config: map[string]string{
			"root": handler.root,
		},
	}, nil
}
