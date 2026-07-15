package blob

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

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

type stubHead struct {
	out *s3.HeadObjectOutput
	err error
}

func (s stubHead) HeadObject(_ context.Context, _ *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
	return s.out, s.err
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
		head    stubHead
		want    bool
		wantErr bool
	}{
		{name: "present", head: stubHead{out: &s3.HeadObjectOutput{}}, want: true},
		{name: "typed not found", head: stubHead{err: &types.NotFound{}}, want: false},
		{name: "http 404", head: stubHead{err: responseError(http.StatusNotFound)}, want: false},
		{name: "http 500", head: stubHead{err: responseError(http.StatusInternalServerError)}, wantErr: true},
		{name: "transport error", head: stubHead{err: errors.New("dial tcp")}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{head: tt.head, bucket: testBucket}
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
