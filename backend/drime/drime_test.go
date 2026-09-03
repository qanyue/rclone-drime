// Drime filesystem interface
package drime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rclone/rclone/backend/drime/api"
	"github.com/rclone/rclone/fs"
	"github.com/rclone/rclone/fs/hash"
	"github.com/rclone/rclone/fs/object"
	"github.com/rclone/rclone/fstest/fstests"
	"github.com/rclone/rclone/lib/pacer"
	"github.com/rclone/rclone/lib/rest"
	"github.com/stretchr/testify/require"
)

type objectWithoutHash struct {
	*object.MemoryObject
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
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

func TestListAllGetsIncompleteItemModel(t *testing.T) {
	ctx := context.Background()
	responses := []string{
		`{"data":[{"id":123,"name":"file.txt","type":"text"}],"current_page":1,"last_page":1}`,
		`{"fileEntry":{"id":123,"name":"file.txt","type":"text","file_size":6,"parent_id":1,"updated_at":"2025-01-02T03:04:05Z","client_last_modified":1577851200000,"mime":"text/plain","file_hash":null,"url":"api/v1/file-entries/123"}}`,
	}
	requests := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if requests == 0 {
			require.Equal(t, "Mozilla/5.0", r.UserAgent())
		} else {
			require.Equal(t, "/drive/api/v1/file-entries/123/model", r.URL.Path)
		}
		response := responses[requests]
		requests++
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(response)),
		}, nil
	})}
	f := &Fs{
		opt:   Options{ListChunk: 1000},
		srv:   rest.NewClient(client).SetRoot(rootURL),
		pacer: fs.NewPacer(ctx, pacer.NewDefault()),
	}
	var got *api.Item
	found, err := f.listAll(ctx, "1", false, true, "", func(item *api.Item) bool {
		require.NoError(t, f.verifyMetadata(ctx, item))
		got = item
		return true
	})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 2, requests)
	require.Equal(t, int64(1577851200000), *got.ClientLastModified)

	requests = 0
	found, err = f.listAll(ctx, "1", false, true, "", func(item *api.Item) bool {
		got = item
		return true
	})
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, 1, requests)
	require.Nil(t, got.ClientLastModified)
	ok, missing := got.HasRequiredMetadata()
	require.False(t, ok)
	require.Contains(t, missing, "client_last_modified")
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

func TestVerifyIntegrity(t *testing.T) {
	ctx := context.Background()
	wantHash := strings.Repeat("a", 64)
	for _, test := range []struct {
		name     string
		response string
		wantErr  string
	}{
		{name: "verified", response: `{"status":"success","verified":true,"serverHash":"` + wantHash + `"}`},
		{name: "mismatch", response: `{"status":"success","verified":false,"serverHash":"` + strings.Repeat("b", 64) + `"}`, wantErr: "integrity verification failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				require.Equal(t, "/api/v1/file-entries/123/verify-integrity", r.URL.Path)
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.JSONEq(t, `{"sha256":"`+wantHash+`"}`, string(body))
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     make(http.Header),
					Body:       io.NopCloser(strings.NewReader(test.response)),
				}, nil
			})}
			f := &Fs{
				srv:   rest.NewClient(client).SetRoot(rootURL),
				pacer: fs.NewPacer(ctx, pacer.NewDefault()),
			}
			err := f.verifyIntegrity(ctx, "123", wantHash)
			if test.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, test.wantErr)
			}
		})
	}
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
