package storage

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// S3Client is a generic S3-compatible client for Huawei OBS and similar providers.
// Objects are uploaded with public-read ACL so they are accessible without auth.
type S3Client struct {
	client   *s3.Client
	bucket   string
	endpoint string // bare hostname, e.g. "obs.ap-southeast-3.myhuaweicloud.com"
}

// NewS3Client creates an S3Client for any S3-compatible endpoint.
//
// endpoint should be the bare hostname without scheme, e.g.
// "obs.ap-southeast-3.myhuaweicloud.com". Scheme is stripped if provided.
// region can be empty; defaults to "us-east-1" for compatibility.
func NewS3Client(endpoint, bucket, region, accessKeyID, secretKey string) (*S3Client, error) {
	if endpoint == "" || bucket == "" || accessKeyID == "" || secretKey == "" {
		return nil, fmt.Errorf("storage: endpoint, bucket, accessKeyID and secretKey are required")
	}
	if region == "" {
		region = "us-east-1"
	}
	// Strip scheme if provided so we always work with a bare hostname.
	endpoint = strings.TrimPrefix(strings.TrimPrefix(endpoint, "https://"), "http://")

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKeyID, secretKey, ""),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: init S3 config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String("https://" + endpoint)
		// Virtual-hosted style: https://{bucket}.{endpoint}/{key}
		// Huawei OBS requires this; path-style URLs serve the object but
		// the public URL must be virtual-hosted for CDN/browser access.
		o.UsePathStyle = false
		// Huawei OBS does not support aws-chunked encoding or automatic
		// SHA256 payload computation — disable optional checksums.
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})

	return &S3Client{client: client, bucket: bucket, endpoint: endpoint}, nil
}

// UploadBytes stores raw bytes under key with the given content type.
// The object is uploaded with public-read ACL so GitHub's camo proxy can render it inline.
func (c *S3Client) UploadBytes(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(c.bucket),
		Key:           aws.String(key),
		Body:          bytes.NewReader(data),
		ContentType:   aws.String(contentType),
		ContentLength: aws.Int64(int64(len(data))),
		ACL:           s3types.ObjectCannedACLPublicRead,
	}, s3.WithAPIOptions(v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware))
	if err != nil {
		return fmt.Errorf("s3: upload %q: %w", key, err)
	}
	return nil
}

// PublicURL returns the virtual-hosted public URL for an uploaded object:
// https://{bucket}.{endpoint}/{key}
func (c *S3Client) PublicURL(key string) string {
	return fmt.Sprintf("https://%s.%s/%s", c.bucket, c.endpoint, key)
}
