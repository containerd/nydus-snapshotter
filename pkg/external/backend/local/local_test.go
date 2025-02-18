package local

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/vmihailenco/msgpack/v5"
)

type Object2 struct {
	Kind   string `msgpack:"k"`
	Number uint64 `msgpack:"n"`
	Path   string `msgpack:"p"`
}

func TestSerializeCompatibility(t *testing.T) {
	object1 := object{
		Path: "test1",
	}
	object2 := Object2{}
	buf, err := msgpack.Marshal(&object1)
	require.NoError(t, err)
	err = msgpack.Unmarshal(buf, &object2)
	require.NoError(t, err)
	require.Equal(t, object1.Path, object2.Path)

	object1 = object{}
	object2 = Object2{
		Kind:   "test2",
		Number: 123,
		Path:   "test1",
	}
	object3 := Object2{}
	buf, err = msgpack.Marshal(&object2)
	require.NoError(t, err)
	err = msgpack.Unmarshal(buf, &object1)
	require.NoError(t, err)
	require.Equal(t, object2.Path, object1.Path)

	err = msgpack.Unmarshal(buf, &object3)
	require.NoError(t, err)
	require.Equal(t, object2, object3)
}
