// Drime filesystem interface
package drime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

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
