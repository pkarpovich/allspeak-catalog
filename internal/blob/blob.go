package blob

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// PresignExpiry is how long presigned upload and download URLs stay valid. It
// is the single source of truth for the reported urlsExpireAt in read responses.
const PresignExpiry = time.Hour

// Config holds the R2 (S3-compatible) connection parameters.
type Config struct {
	Endpoint string
	KeyID    string
	Secret   string
	Bucket   string
}

type headAPI interface {
	HeadObject(ctx context.Context, params *s3.HeadObjectInput, optFns ...func(*s3.Options)) (*s3.HeadObjectOutput, error)
}

// Client presigns R2 object transfers and checks object existence. It never
// moves object bytes itself.
type Client struct {
	presign *s3.PresignClient
	head    headAPI
	bucket  string
}

// New builds a Client for Cloudflare R2 from cfg. Static credentials, region
// "auto", and a custom endpoint are pinned for R2; default payload checksums are
// disabled so presigned PUTs stay signable by a plain curl upload.
func New(cfg Config) (*Client, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("auto"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.KeyID, cfg.Secret, "")),
		awsconfig.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
		awsconfig.WithResponseChecksumValidation(aws.ResponseChecksumValidationWhenRequired),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}
	s3c := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(cfg.Endpoint)
	})
	return &Client{
		presign: s3.NewPresignClient(s3c),
		head:    s3c,
		bucket:  cfg.Bucket,
	}, nil
}

// Key returns the content-addressed R2 object key for a file. filename must be
// pre-sanitized (see manifest.SanitizeFilename); read paths use it verbatim.
func Key(sessionID, sha256, filename string) string {
	return fmt.Sprintf("sessions/%s/files/%s-%s", sessionID, sha256, filename)
}

// PresignPut returns a presigned PUT URL for key, valid for one hour.
func (c *Client) PresignPut(ctx context.Context, key string) (string, error) {
	req, err := c.presign.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(PresignExpiry))
	if err != nil {
		return "", fmt.Errorf("presign put %q: %w", key, err)
	}
	return req.URL, nil
}

// PresignGet returns a presigned GET URL for key, valid for one hour.
func (c *Client) PresignGet(ctx context.Context, key string) (string, error) {
	req, err := c.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(PresignExpiry))
	if err != nil {
		return "", fmt.Errorf("presign get %q: %w", key, err)
	}
	return req.URL, nil
}

// Exists reports whether key is present in the bucket, mapping a not-found
// response to (false, nil) and any other failure to an error.
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	_, err := c.head.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}
	var notFound *types.NotFound
	if errors.As(err, &notFound) {
		return false, nil
	}
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) && respErr.HTTPStatusCode() == http.StatusNotFound {
		return false, nil
	}
	return false, fmt.Errorf("head object %q: %w", key, err)
}
