// Bucket operations use the AWS SDK and never bypass retention protections.
// Purging repeatedly removes the first page, avoiding skipped entries during mutation.
package rustfsapi

import (
	"context"
	"errors"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/fulcrumproject/agent-s3/internal/s3agent"
)

func (c *Client) BucketTags(ctx context.Context, bucket string) (map[string]string, error) {
	result, err := c.s3.GetBucketTagging(ctx, &s3.GetBucketTaggingInput{Bucket: aws.String(bucket)})
	if err != nil {
		var api smithy.APIError
		if errors.As(err, &api) && api.ErrorCode() == "NoSuchTagSet" {
			return map[string]string{}, nil
		}
		return nil, sdkError(ctx, "read bucket ownership", err)
	}
	tags := make(map[string]string, len(result.TagSet))
	for _, tag := range result.TagSet {
		tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	return tags, nil
}
func (c *Client) CreateBucket(ctx context.Context, bucket string) error {
	input := &s3.CreateBucketInput{Bucket: aws.String(bucket)}
	if c.cfg.Region != "us-east-1" {
		input.CreateBucketConfiguration = &types.CreateBucketConfiguration{LocationConstraint: types.BucketLocationConstraint(c.cfg.Region)}
	}
	_, err := c.s3.CreateBucket(ctx, input)
	return sdkError(ctx, "create bucket", err)
}
func (c *Client) PutBucketTags(ctx context.Context, bucket string, tags map[string]string) error {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	set := make([]types.Tag, 0, len(tags))
	for _, key := range keys {
		set = append(set, types.Tag{Key: aws.String(key), Value: aws.String(tags[key])})
	}
	_, err := c.s3.PutBucketTagging(ctx, &s3.PutBucketTaggingInput{Bucket: aws.String(bucket), Tagging: &types.Tagging{TagSet: set}})
	return sdkError(ctx, "write bucket ownership", err)
}
func (c *Client) DeleteBucket(ctx context.Context, bucket string) error {
	_, err := c.s3.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
	return sdkError(ctx, "delete bucket", err)
}
func (c *Client) PurgeBucket(ctx context.Context, bucket string) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		uploads, err := c.s3.ListMultipartUploads(ctx, &s3.ListMultipartUploadsInput{Bucket: aws.String(bucket), MaxUploads: aws.Int32(1000)})
		if err != nil {
			return sdkError(ctx, "list incomplete uploads", err)
		}
		if len(uploads.Uploads) == 0 {
			if aws.ToBool(uploads.IsTruncated) {
				return errors.New("invalid multipart pagination")
			}
			break
		}
		for _, upload := range uploads.Uploads {
			if upload.Key == nil || upload.UploadId == nil {
				return errors.New("incomplete multipart metadata")
			}
			_, err = c.s3.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String(bucket), Key: upload.Key, UploadId: upload.UploadId})
			safe := sdkError(ctx, "abort incomplete upload", err)
			if safe != nil && !errors.Is(safe, s3agent.ErrNotFound) {
				return safe
			}
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		page, err := c.s3.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(bucket), MaxKeys: aws.Int32(1000)})
		if err != nil {
			return sdkError(ctx, "list object versions", err)
		}
		objects := make([]types.ObjectIdentifier, 0, len(page.Versions)+len(page.DeleteMarkers))
		for _, object := range page.Versions {
			if object.Key == nil || object.VersionId == nil {
				return errors.New("incomplete object version metadata")
			}
			objects = append(objects, types.ObjectIdentifier{Key: object.Key, VersionId: object.VersionId})
		}
		for _, marker := range page.DeleteMarkers {
			if marker.Key == nil || marker.VersionId == nil {
				return errors.New("incomplete delete marker metadata")
			}
			objects = append(objects, types.ObjectIdentifier{Key: marker.Key, VersionId: marker.VersionId})
		}
		if len(objects) == 0 {
			if aws.ToBool(page.IsTruncated) {
				return errors.New("invalid version pagination")
			}
			return nil
		}
		if len(objects) > 1000 {
			return errors.New("backend exceeded deletion batch size")
		}
		result, err := c.s3.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket), Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)},
			// No BypassGovernanceRetention header: retention is never overridden.
		})
		if err != nil {
			return sdkError(ctx, "delete object versions", err)
		}
		if len(result.Errors) > 0 {
			return errors.New("backend refused one or more object deletions; data may be retained")
		}
	}
}
