// Drime filesystem interface
package drime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"
	"time"

	"github.com/rclone/rclone/backend/drime/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/fs/object"
	"github.com/rclone/rclone/fstest/fstests"
	"github.com/stretchr/testify/require"
)

type objectWithoutHash struct {
	*object.MemoryObject
}

func (o *objectWithoutHash) Hash(context.Context, hash.Type) (string, error) {
	return "", hash.ErrUnsupported
}

func TestSourceSHA256ReopensSource(t *testing.T) {
	contents := []byte("firstsecondthird")
	src := &objectWithoutHash{MemoryObject: object.NewMemoryObject("test", time.Now(), contents)}

	got, err := sourceSHA256(context.Background(), src)
	require.NoError(t, err)
	want := sha256.Sum256(contents)
	require.Equal(t, hex.EncodeToString(want[:]), got)
}

func TestSourceSHA256UsesAvailableHash(t *testing.T) {
	const want = "0123456789abcdef"
	src := object.NewStaticObjectInfo("test", time.Now(), 0, true, map[hash.Type]string{
		hash.SHA256: want,
	}, nil)

	got, err := sourceSHA256(context.Background(), src)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestSourceSHA256AllowsNonReopenableSource(t *testing.T) {
	src := object.NewStaticObjectInfo("test", time.Now(), -1, true, nil, nil)

	got, err := sourceSHA256(context.Background(), src)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestObjectHash(t *testing.T) {
	o := &Object{fileHash: "abc123"}
	got, err := o.Hash(context.Background(), hash.SHA256)
	require.NoError(t, err)
	require.Equal(t, "abc123", got)
	_, err = o.Hash(context.Background(), hash.MD5)
	require.ErrorIs(t, err, hash.ErrUnsupported)
}

func TestObjectModTime(t *testing.T) {
	clientLastModified := time.Date(2024, time.January, 2, 3, 4, 5, 678000000, time.UTC).UnixMilli()
	o := &Object{}
	o.setMetaDataAny(&api.Item{
		UpdatedAt:          time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC),
		ClientLastModified: &clientLastModified,
	})
	require.Equal(t, time.UnixMilli(clientLastModified), o.ModTime(context.Background()))

	o.setMetaDataAny(&api.Item{UpdatedAt: time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC)})
	require.Equal(t, time.Date(2025, time.January, 2, 3, 4, 5, 0, time.UTC), o.ModTime(context.Background()))
}

func TestHashChunk(t *testing.T) {
	fileHasher, err := hash.NewMultiHasherTypes(hash.Set(hash.SHA256))
	require.NoError(t, err)
	s := &drimeChunkWriter{fileHasher: fileHasher}
	s.hashCond = sync.NewCond(&s.hashMu)
	first := bytes.NewReader([]byte("first"))
	second := bytes.NewReader([]byte("second"))

	md5sum, _, err := s.hashChunk(first, 0)
	require.NoError(t, err)
	require.Equal(t, "8b04d5e3775d298e78455efc5ca404d5", hex.EncodeToString(md5sum))
	md5sum, _, err = s.hashChunk(second, 1)
	require.NoError(t, err)
	require.Equal(t, "a9f0e61a137d86aa9db53465e0801612", hex.EncodeToString(md5sum))
	require.Equal(t, 5, first.Len())
	require.Equal(t, 6, second.Len())
	want := sha256.Sum256([]byte("firstsecond"))
	require.Equal(t, hex.EncodeToString(want[:]), fileHasher.Sums()[hash.SHA256])
}

// TestIntegration runs integration tests against the remote
func TestIntegration(t *testing.T) {
	fstests.Run(t, &fstests.Opt{
		RemoteName: "TestDrime:",
		NilObject:  (*Object)(nil),
		ChunkedUpload: fstests.ChunkedUploadConfig{
			MinChunkSize: minChunkSize,
		},
	})
}

func (f *Fs) SetUploadChunkSize(cs fs.SizeSuffix) (fs.SizeSuffix, error) {
	return f.setUploadChunkSize(cs)
}

func (f *Fs) SetUploadCutoff(cs fs.SizeSuffix) (fs.SizeSuffix, error) {
	return f.setUploadCutoff(cs)
}

var (
	_ fstests.SetUploadChunkSizer = (*Fs)(nil)
	_ fstests.SetUploadCutoffer   = (*Fs)(nil)
)
