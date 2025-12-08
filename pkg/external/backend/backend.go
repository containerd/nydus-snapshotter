package backend

import "context"

const (
	DefaultFileChunkSize    = 1024 * 1024 * 1 // 1 MB
	DefaultThrottleFileSize = 1024 * 1024 * 2 // 2 MB
)

type Backend struct {
	Type   string            `json:"type"`
	Config map[string]string `json:"config"`
}

type Result struct {
	Chunks  []Chunk
	Files   []string
	Backend Backend
}

type File struct {
	RelativePath string
	Size         int64
}

// Handler is the interface for backend handler.
type Handler interface {
	// Backend returns the backend information.
	Backend(ctx context.Context) (*Backend, error)
	// Handle handles the file and returns the object information.
	Handle(ctx context.Context, file File) ([]Chunk, error)
}

type Chunk interface {
	ObjectID() uint32
	ObjectContent() interface{}
	ObjectOffset() uint64
}

// SplitObjectOffsets splits the total size into object offsets
// with the specified chunk size.
func SplitObjectOffsets(totalSize, chunkSize int64) []uint64 {
	objectOffsets := []uint64{}
	if chunkSize <= 0 {
		return objectOffsets
	}

	chunkN := totalSize / chunkSize

	for i := int64(0); i < chunkN; i++ {
		objectOffsets = append(objectOffsets, uint64(i*chunkSize))
	}

	if totalSize%chunkSize > 0 {
		objectOffsets = append(objectOffsets, uint64(chunkN*chunkSize))
	}

	return objectOffsets
}
