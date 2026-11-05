package blob

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testEndpoint = "https://accountid.r2.cloudflarestorage.com"
	testBucket   = "allspeak"
	testSHA      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

func newTestClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(Config{
		Endpoint: testEndpoint,
		KeyID:    "test-key-id",
		Secret:   "test-secret",
		Bucket:   testBucket,
	})
	require.NoError(t, err)
	return c
}

func TestKey(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		sha256    string
		filename  string
		want      string
	}{
		{
			name:      "audio track",
			sessionID: "sess1",
			sha256:    testSHA,
			filename:  "film.m4a",
			want:      "sessions/sess1/files/" + testSHA + "-film.m4a",
		},
		{
			name:      "subtitle",
			sessionID: "de305d54-75b4-431b-adb2-eb6b9e546013",
			sha256:    testSHA,
			filename:  "film.srt",
			want:      "sessions/de305d54-75b4-431b-adb2-eb6b9e546013/files/" + testSHA + "-film.srt",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Key(tt.sessionID, tt.sha256, tt.filename))
		})
	}
}

func TestPresign(t *testing.T) {
	c := newTestClient(t)
	key := Key("sess1", testSHA, "film.m4a")

	tests := []struct {
		name    string
		presign func(context.Context, string) (string, error)
	}{
		{name: "put", presign: c.PresignPut},
		{name: "get", presign: c.PresignGet},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.presign(context.Background(), key)
			require.NoError(t, err)

			u, err := url.Parse(got)
			require.NoError(t, err)
			assert.Contains(t, u.Host, testBucket)
			assert.Contains(t, u.Path, key)
			assert.Equal(t, "3600", u.Query().Get("X-Amz-Expires"))
			assert.NotEmpty(t, u.Query().Get("X-Amz-Signature"))
		})
	}
}

func TestPresignPutHasNoChecksum(t *testing.T) {
	c := newTestClient(t)
	key := Key("sess1", testSHA, "film.m4a")

	req, err := c.presign.PresignPutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(testBucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(PresignExpiry))
	require.NoError(t, err)

	for name := range req.SignedHeader {
		assert.NotContains(t, strings.ToLower(name), "x-amz-checksum",
			"signed header %q must not carry a checksum", name)
	}

	u, err := url.Parse(req.URL)
	require.NoError(t, err)
	for name := range u.Query() {
		assert.NotContains(t, strings.ToLower(name), "x-amz-checksum",
			"query param %q must not carry a checksum", name)
	}
}

type stubS3 struct {
	headOut *s3.HeadObjectOutput
	headErr error
	putErr  error
	put     *s3.PutObjectInput
	pages   []*s3.ListObjectsV2Output
	listed  []*s3.ListObjectsV2Input
	listErr error
}

func (s *stubS3) HeadObject(_ context.Context, _ *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return s.headOut, s.headErr
}

func (s *stubS3) PutObject(_ context.Context, in *s3.PutObjectInput, _ ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	s.put = in
	return &s3.PutObjectOutput{}, s.putErr
}

func (s *stubS3) ListObjectsV2(_ context.Context, in *s3.ListObjectsV2Input, _ ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	s.listed = append(s.listed, in)
	if s.listErr != nil {
		return nil, s.listErr
	}
	page := s.pages[len(s.listed)-1]
	return page, nil
}

func responseError(status int) error {
	return &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status}},
		Err:      errors.New("boom"),
	}
}

func TestExists(t *testing.T) {
	tests := []struct {
		name    string
		head    *stubS3
		want    bool
		wantErr bool
	}{
		{name: "present", head: &stubS3{headOut: &s3.HeadObjectOutput{}}, want: true},
		{name: "typed not found", head: &stubS3{headErr: &types.NotFound{}}, want: false},
		{name: "http 404", head: &stubS3{headErr: responseError(http.StatusNotFound)}, want: false},
		{name: "http 500", head: &stubS3{headErr: responseError(http.StatusInternalServerError)}, wantErr: true},
		{name: "transport error", head: &stubS3{headErr: errors.New("dial tcp")}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{s3: tt.head, bucket: testBucket}
			got, err := c.Exists(context.Background(), "sessions/sess1/files/obj")
			if tt.wantErr {
				require.Error(t, err)
				assert.False(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPut(t *testing.T) {
	stub := &stubS3{}
	c := &Client{s3: stub, bucket: testBucket}

	err := c.Put(context.Background(), "diagnostics/sess1/log.jsonl", "application/x-ndjson", []byte("{}\n"))

	require.NoError(t, err)
	require.NotNil(t, stub.put)
	assert.Equal(t, testBucket, aws.ToString(stub.put.Bucket))
	assert.Equal(t, "diagnostics/sess1/log.jsonl", aws.ToString(stub.put.Key))
	assert.Equal(t, "application/x-ndjson", aws.ToString(stub.put.ContentType))
	assert.Equal(t, int64(3), aws.ToInt64(stub.put.ContentLength))
}

func TestPutError(t *testing.T) {
	c := &Client{s3: &stubS3{putErr: errors.New("boom")}, bucket: testBucket}

	err := c.Put(context.Background(), "diagnostics/sess1/log.jsonl", "application/x-ndjson", []byte("x"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "diagnostics/sess1/log.jsonl")
}

func TestListFollowsPages(t *testing.T) {
	first := time.Date(2026, 11, 4, 19, 0, 0, 0, time.UTC)
	stub := &stubS3{pages: []*s3.ListObjectsV2Output{
		{
			Contents:              []types.Object{{Key: aws.String("diagnostics/s/a.jsonl"), Size: aws.Int64(10), LastModified: aws.Time(first)}},
			IsTruncated:           aws.Bool(true),
			NextContinuationToken: aws.String("next"),
		},
		{
			Contents: []types.Object{{Key: aws.String("diagnostics/s/b.jsonl"), Size: aws.Int64(20), LastModified: aws.Time(first.Add(time.Hour))}},
		},
	}}
	c := &Client{s3: stub, bucket: testBucket}

	objects, err := c.List(context.Background(), "diagnostics/s/")

	require.NoError(t, err)
	assert.Equal(t, []Object{
		{Key: "diagnostics/s/a.jsonl", Size: 10, LastModified: first},
		{Key: "diagnostics/s/b.jsonl", Size: 20, LastModified: first.Add(time.Hour)},
	}, objects)
	require.Len(t, stub.listed, 2)
	assert.Equal(t, "diagnostics/s/", aws.ToString(stub.listed[0].Prefix))
	assert.Equal(t, "next", aws.ToString(stub.listed[1].ContinuationToken))
}

func TestListError(t *testing.T) {
	c := &Client{s3: &stubS3{listErr: errors.New("boom")}, bucket: testBucket}

	_, err := c.List(context.Background(), "diagnostics/s/")

	require.Error(t, err)
}
