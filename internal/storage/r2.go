// Package storage provides Cloudflare R2 screenshot storage via the S3-compatible API.
package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// R2Client wraps an S3-compatible client pointed at Cloudflare R2.
type R2Client struct {
	client     *s3.Client
	bucketName string
}

// NewR2Client constructs an R2Client using the given Cloudflare account credentials.
// The R2 endpoint is derived from accountID:
//
//	https://{accountID}.r2.cloudflarestorage.com
//
// Pass an empty bucket to use the default bucket name "fixloop-screenshots-prod".
func NewR2Client(accountID, accessKeyID, secretKey, bucket string) (*R2Client, error) {
	if accountID == "" || accessKeyID == "" || secretKey == "" {
		return nil, fmt.Errorf("storage: accountID, accessKeyID, and secretKey are all required")
	}
	if bucket == "" {
		bucket = "fixloop-screenshots-prod"
	}

	endpoint := fmt.Sprintf("https://%s.r2.cloudflarestorage.com", accountID)
	client := s3.New(s3.Options{
		Region:       "auto",
		Credentials:  credentials.NewStaticCredentialsProvider(accessKeyID, secretKey, ""),
		UsePathStyle: false,
		BaseEndpoint: aws.String(endpoint),
	})
	return &R2Client{client: client, bucketName: bucket}, nil
}

// Disabled reports whether the client is nil (i.e. R2 is not configured).
// Callers should check this before attempting uploads.
func (c *R2Client) Disabled() bool {
	return c == nil
}

// Upload reads the file at filePath and stores it in R2 under key.
// The Content-Type is set to "image/png" for .png files; otherwise
// "application/octet-stream" is used.
func (c *R2Client) Upload(ctx context.Context, key string, filePath string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("storage: open %q: %w", filePath, err)
	}
	defer f.Close()

	contentType := "application/octet-stream"
	if filepath.Ext(filePath) == ".png" {
		contentType = "image/png"
	}

	_, err = c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucketName),
		Key:         aws.String(key),
		Body:        f,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("storage: upload %q: %w", key, err)
	}
	return nil
}

// GetObject returns a streaming reader for the R2 object identified by key.
// The caller is responsible for closing the returned ReadCloser.
func (c *R2Client) GetObject(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("storage: get object %q: %w", key, err)
	}
	return out.Body, nil
}

// Download retrieves an object from R2 and returns a ReadCloser + content type.
// Caller must close the reader.
func (c *R2Client) Download(ctx context.Context, key string) (io.ReadCloser, string, error) {
	out, err := c.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, "", fmt.Errorf("r2 download %s: %w", key, err)
	}
	ct := ""
	if out.ContentType != nil {
		ct = *out.ContentType
	}
	return out.Body, ct, nil
}

// UploadBytes stores raw bytes in R2 under key with the given content type.
func (c *R2Client) UploadBytes(ctx context.Context, key string, data []byte, contentType string) error {
	_, err := c.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucketName),
		Key:         aws.String(key),
		Body:        bytes.NewReader(data),
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("storage: upload bytes %q: %w", key, err)
	}
	return nil
}

// PresignURL generates a pre-signed GET URL for key, valid for ttl.
func (c *R2Client) PresignURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	presigner := s3.NewPresignClient(c.client)
	req, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucketName),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("storage: presign %q: %w", key, err)
	}
	return req.URL, nil
}

// KeyForScreenshot builds the canonical R2 key for a screenshot:
//
//	{userID}/{projectID}/{runID}/{scenarioID}_{step}_{ts.Unix()}.png
func KeyForScreenshot(userID, projectID, runID, scenarioID int64, step int, ts time.Time) string {
	return fmt.Sprintf("%d/%d/%d/%d_%d_%d.png", userID, projectID, runID, scenarioID, step, ts.Unix())
}
