/*
* Copyright 2021 Layotto Authors
*
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You may obtain a copy of the License at
*
*     http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
 */

package ceph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	aws_config "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/jinzhu/copier"
	"mosn.io/pkg/log"

	"mosn.io/layotto/components/oss"
	"mosn.io/layotto/components/pkg/utils"
)

type CephOss struct {
	client    *s3.Client
	basicConf json.RawMessage
}

func NewCephOss() oss.Oss {
	return &CephOss{}
}

func (c *CephOss) Init(ctx context.Context, config *oss.Config) error {
	c.basicConf = config.Metadata[oss.BasicConfiguration]
	m := &utils.OssMetadata{}
	err := json.Unmarshal(c.basicConf, &m)
	if err != nil {
		return oss.ErrInvalid
	}

	customResolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
		return aws.Endpoint{
			URL: m.Endpoint,
		}, nil
	})
	optFunc := []func(options *aws_config.LoadOptions) error{
		aws_config.WithRegion(m.Region),
		aws_config.WithCredentialsProvider(credentials.StaticCredentialsProvider{
			Value: aws.Credentials{
				AccessKeyID: m.AccessKeyID, SecretAccessKey: m.AccessKeySecret,
				Source: "provider",
			},
		}),
		aws_config.WithEndpointResolverWithOptions(customResolver),
	}
	cfg, err := aws_config.LoadDefaultConfig(context.TODO(), optFunc...)
	if err != nil {
		return err
	}
	client := s3.NewFromConfig(cfg, func(options *s3.Options) {
		options.UsePathStyle = true
	})
	c.client = client
	return nil
}

func (c *CephOss) GetObject(ctx context.Context, req *oss.GetObjectInput) (*oss.GetObjectOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.GetObjectInput{}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	ob, err := client.GetObject(context.TODO(), input)
	if err != nil {
		return nil, err
	}

	out := &oss.GetObjectOutput{}
	err = copier.Copy(out, ob)
	if err != nil {
		return nil, err
	}
	out.DataStream = ob.Body
	return out, nil
}

func (c *CephOss) PutObject(ctx context.Context, req *oss.PutObjectInput) (*oss.PutObjectOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.PutObjectInput{}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	input.Body = req.DataStream
	uploader := manager.NewUploader(client)
	resp, err := uploader.Upload(context.TODO(), input)
	if err != nil {
		return nil, err
	}

	out := &oss.PutObjectOutput{}
	err = copier.Copy(out, resp)
	if err != nil {
		return nil, err
	}
	return out, err
}

func (c *CephOss) DeleteObject(ctx context.Context, req *oss.DeleteObjectInput) (*oss.DeleteObjectOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.DeleteObjectInput{
		Bucket: &req.Bucket,
		Key:    &req.Key,
	}
	resp, err := client.DeleteObject(ctx, input)
	if err != nil {
		return nil, err
	}

	versionId := ""
	if resp.VersionId != nil {
		versionId = *resp.VersionId
	}
	return &oss.DeleteObjectOutput{DeleteMarker: resp.DeleteMarker, RequestCharged: string(resp.RequestCharged), VersionId: versionId}, err
}

func (c *CephOss) PutObjectTagging(ctx context.Context, req *oss.PutObjectTaggingInput) (*oss.PutObjectTaggingOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.PutObjectTaggingInput{Tagging: &types.Tagging{}}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	for k, v := range req.Tags {
		k, v := k, v
		input.Tagging.TagSet = append(input.Tagging.TagSet, types.Tag{Key: &k, Value: &v})
	}
	_, err = client.PutObjectTagging(ctx, input)

	return &oss.PutObjectTaggingOutput{}, err
}

func (c *CephOss) DeleteObjectTagging(ctx context.Context, req *oss.DeleteObjectTaggingInput) (*oss.DeleteObjectTaggingOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.DeleteObjectTaggingInput{}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	resp, err := client.DeleteObjectTagging(ctx, input)
	if err != nil {
		return nil, err
	}

	versionId := ""
	if resp.VersionId != nil {
		versionId = *resp.VersionId
	}
	return &oss.DeleteObjectTaggingOutput{VersionId: versionId}, err
}

func (c *CephOss) GetObjectTagging(ctx context.Context, req *oss.GetObjectTaggingInput) (*oss.GetObjectTaggingOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.GetObjectTaggingInput{}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	resp, err := client.GetObjectTagging(ctx, input)
	if err != nil {
		return nil, err
	}

	output := &oss.GetObjectTaggingOutput{Tags: map[string]string{}}
	for _, tags := range resp.TagSet {
		output.Tags[*tags.Key] = *tags.Value
	}
	return output, err
}

func (c *CephOss) CopyObject(ctx context.Context, req *oss.CopyObjectInput) (*oss.CopyObjectOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	if req.CopySource == nil {
		return nil, errors.New("must specific copy_source")
	}

	input := &s3.CopyObjectInput{}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{int642time}})
	if err != nil {
		return nil, err
	}
	copySource := req.CopySource.CopySourceBucket + "/" + req.CopySource.CopySourceKey
	if req.CopySource.CopySourceVersionId != "" {
		copySource += "?versionId=" + req.CopySource.CopySourceVersionId
	}
	input.CopySource = &copySource
	resp, err := client.CopyObject(context.TODO(), input)
	if err != nil {
		return nil, err
	}

	out := &oss.CopyObjectOutput{}
	err = copier.CopyWithOption(out, resp, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	return out, err
}

func (c *CephOss) DeleteObjects(ctx context.Context, req *oss.DeleteObjectsInput) (*oss.DeleteObjectsOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.DeleteObjectsInput{
		Bucket: &req.Bucket,
		Delete: &types.Delete{},
	}
	if req.Delete != nil {
		for _, v := range req.Delete.Objects {
			object := &types.ObjectIdentifier{}
			err = copier.CopyWithOption(object, v, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
			if err != nil {
				return nil, err
			}
			input.Delete.Objects = append(input.Delete.Objects, *object)
		}
	}
	resp, err := client.DeleteObjects(ctx, input)
	if err != nil {
		return nil, err
	}

	output := &oss.DeleteObjectsOutput{}
	copier.Copy(output, resp)
	return output, err
}

func (c *CephOss) ListObjects(ctx context.Context, req *oss.ListObjectsInput) (*oss.ListObjectsOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.ListObjectsInput{}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	resp, err := client.ListObjects(ctx, input)
	if err != nil {
		return nil, err
	}

	output := &oss.ListObjectsOutput{}
	err = copier.CopyWithOption(output, resp, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{time2int64}})
	// if not return NextMarker, use the value of the last Key in the response as the marker
	if output.IsTruncated && output.NextMarker == "" {
		index := len(output.Contents) - 1
		output.NextMarker = output.Contents[index].Key
	}
	return output, err
}

func (c *CephOss) GetObjectCannedAcl(ctx context.Context, req *oss.GetObjectCannedAclInput) (*oss.GetObjectCannedAclOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.GetObjectAclInput{}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	resp, err := client.GetObjectAcl(context.TODO(), input)
	if err != nil {
		return nil, err
	}

	out := &oss.GetObjectCannedAclOutput{}
	err = copier.CopyWithOption(out, resp, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	bs, _ := json.Marshal(resp.Grants)
	var bf bytes.Buffer
	err = json.Indent(&bf, bs, "", "\t")
	if err != nil {
		return nil, err
	}
	out.CannedAcl = bf.String()
	return out, nil
}

func (c *CephOss) PutObjectCannedAcl(ctx context.Context, req *oss.PutObjectCannedAclInput) (*oss.PutObjectCannedAclOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}
	input := &s3.PutObjectAclInput{Bucket: &req.Bucket, Key: &req.Key, ACL: types.ObjectCannedACL(req.Acl)}
	resp, err := client.PutObjectAcl(ctx, input)
	if err != nil {
		return nil, err
	}
	return &oss.PutObjectCannedAclOutput{RequestCharged: string(resp.RequestCharged)}, err
}


func (c *CephOss) CreateMultipartUpload(ctx context.Context, req *oss.CreateMultipartUploadInput) (*oss.CreateMultipartUploadOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.CreateMultipartUploadInput{}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{int642time}})
	if err != nil {
		log.DefaultLogger.Errorf("copy CreateMultipartUploadInput fail, err: %+v", err)
		return nil, err
	}
	resp, err := client.CreateMultipartUpload(ctx, input)
	if err != nil {
		return nil, err
	}

	output := &oss.CreateMultipartUploadOutput{}
	copier.CopyWithOption(output, resp, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{time2int64}})
	return output, err
}

func (c *CephOss) UploadPart(ctx context.Context, req *oss.UploadPartInput) (*oss.UploadPartOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.UploadPartInput{}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	input.Body = req.DataStream
	resp, err := client.UploadPart(ctx, input, s3.WithAPIOptions(v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware))
	if err != nil {
		return nil, err
	}

	output := &oss.UploadPartOutput{}
	err = copier.Copy(output, resp)
	if err != nil {
		return nil, err
	}
	return output, err
}

func (c *CephOss) UploadPartCopy(ctx context.Context, req *oss.UploadPartCopyInput) (*oss.UploadPartCopyOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.UploadPartCopyInput{}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	copySource := req.CopySource.CopySourceBucket + "/" + req.CopySource.CopySourceKey
	if req.CopySource.CopySourceVersionId != "" {
		copySource += "?versionId=" + req.CopySource.CopySourceVersionId
	}
	input.CopySource = &copySource
	resp, err := client.UploadPartCopy(context.TODO(), input)
	if err != nil {
		return nil, err
	}

	out := &oss.UploadPartCopyOutput{}
	err = copier.CopyWithOption(out, resp, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	return out, err
}

func (c *CephOss) CompleteMultipartUpload(ctx context.Context, req *oss.CompleteMultipartUploadInput) (*oss.CompleteMultipartUploadOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.CompleteMultipartUploadInput{MultipartUpload: &types.CompletedMultipartUpload{}}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	resp, err := client.CompleteMultipartUpload(ctx, input)
	if err != nil {
		return nil, err
	}

	output := &oss.CompleteMultipartUploadOutput{}
	err = copier.Copy(output, resp)
	return output, err
}

func (c *CephOss) AbortMultipartUpload(ctx context.Context, req *oss.AbortMultipartUploadInput) (*oss.AbortMultipartUploadOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.AbortMultipartUploadInput{}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	resp, err := client.AbortMultipartUpload(ctx, input)
	if err != nil {
		return nil, err
	}

	output := &oss.AbortMultipartUploadOutput{
		RequestCharged: string(resp.RequestCharged),
	}
	return output, err
}

func (c *CephOss) ListParts(ctx context.Context, req *oss.ListPartsInput) (*oss.ListPartsOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.ListPartsInput{}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	resp, err := client.ListParts(ctx, input)
	if err != nil {
		return nil, err
	}

	output := &oss.ListPartsOutput{}
	err = copier.CopyWithOption(output, resp, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	return output, err
}

func (c *CephOss) ListMultipartUploads(ctx context.Context, req *oss.ListMultipartUploadsInput) (*oss.ListMultipartUploadsOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.ListMultipartUploadsInput{}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	resp, err := client.ListMultipartUploads(ctx, input)
	if err != nil {
		return nil, err
	}

	output := &oss.ListMultipartUploadsOutput{CommonPrefixes: []string{}, Uploads: []*oss.MultipartUpload{}}
	err = copier.Copy(output, resp)
	if err != nil {
		return nil, err
	}
	for _, v := range resp.CommonPrefixes {
		output.CommonPrefixes = append(output.CommonPrefixes, *v.Prefix)
	}
	for _, v := range resp.Uploads {
		upload := &oss.MultipartUpload{}
		copier.CopyWithOption(upload, v, copier.Option{IgnoreEmpty: true, DeepCopy: true})
		output.Uploads = append(output.Uploads, upload)
	}
	return output, err
}

func (c *CephOss) ListObjectVersions(ctx context.Context, req *oss.ListObjectVersionsInput) (*oss.ListObjectVersionsOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}

	input := &s3.ListObjectVersionsInput{}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	resp, err := client.ListObjectVersions(ctx, input)
	if err != nil {
		return nil, err
	}

	output := &oss.ListObjectVersionsOutput{}
	err = copier.Copy(output, resp)
	if err != nil {
		return nil, err
	}
	for _, v := range resp.CommonPrefixes {
		output.CommonPrefixes = append(output.CommonPrefixes, *v.Prefix)
	}
	for _, v := range resp.DeleteMarkers {
		entry := &oss.DeleteMarkerEntry{IsLatest: v.IsLatest, Key: *v.Key, Owner: &oss.Owner{DisplayName: *v.Owner.DisplayName, ID: *v.Owner.ID}, VersionId: *v.VersionId}
		output.DeleteMarkers = append(output.DeleteMarkers, entry)
	}
	for _, v := range resp.Versions {
		version := &oss.ObjectVersion{}
		copier.CopyWithOption(version, v, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{time2int64}})
		output.Versions = append(output.Versions, version)
	}
	return output, err
}

func (c *CephOss) HeadObject(ctx context.Context, req *oss.HeadObjectInput) (*oss.HeadObjectOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}
	input := &s3.HeadObjectInput{}
	err = copier.CopyWithOption(input, req, copier.Option{IgnoreEmpty: true, DeepCopy: true, Converters: []copier.TypeConverter{}})
	if err != nil {
		return nil, err
	}
	resp, err := client.HeadObject(ctx, input)
	if err != nil {
		return nil, err
	}
	return &oss.HeadObjectOutput{ResultMetadata: resp.Metadata}, nil
}

func (c *CephOss) IsObjectExist(ctx context.Context, req *oss.IsObjectExistInput) (*oss.IsObjectExistOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}
	input := &s3.HeadObjectInput{Bucket: &req.Bucket, Key: &req.Key}
	_, err = client.HeadObject(ctx, input)
	if err != nil {
		errorMsg := err.Error()
		if strings.Contains(errorMsg, "StatusCode: 404") {
			return &oss.IsObjectExistOutput{FileExist: false}, nil
		}
		return nil, err
	}
	return &oss.IsObjectExistOutput{FileExist: true}, nil
}

func (c *CephOss) SignURL(ctx context.Context, req *oss.SignURLInput) (*oss.SignURLOutput, error) {
	client, err := c.getClient()
	if err != nil {
		return nil, err
	}
	resignClient := s3.NewPresignClient(client)
	switch strings.ToUpper(req.Method) {
	case "GET":
		input := &s3.GetObjectInput{Bucket: &req.Bucket, Key: &req.Key}
		resp, err := resignClient.PresignGetObject(ctx, input, s3.WithPresignExpires(time.Duration((req.ExpiredInSec)*int64(time.Second))))
		if err != nil {
			return nil, err
		}
		return &oss.SignURLOutput{SignedUrl: resp.URL}, nil
	case "PUT":
		input := &s3.PutObjectInput{Bucket: &req.Bucket, Key: &req.Key}
		resp, err := resignClient.PresignPutObject(ctx, input, s3.WithPresignExpires(time.Duration(req.ExpiredInSec*int64(time.Second))))
		if err != nil {
			return nil, err
		}
		return &oss.SignURLOutput{SignedUrl: resp.URL}, nil
	default:
		return nil, fmt.Errorf("not supported method %+v now", req.Method)
	}
}

func (c *CephOss) RestoreObject(ctx context.Context, req *oss.RestoreObjectInput) (*oss.RestoreObjectOutput, error) {
	return nil, errors.New("RestoreObject method not supported on AWS")
}

func (c *CephOss) UpdateDownloadBandwidthRateLimit(ctx context.Context, req *oss.UpdateBandwidthRateLimitInput) error {
	return errors.New("UpdateDownloadBandwidthRateLimit method not supported now")
}

func (c *CephOss) UpdateUploadBandwidthRateLimit(ctx context.Context, req *oss.UpdateBandwidthRateLimitInput) error {
	return errors.New("UpdateUploadBandwidthRateLimit method not supported now")
}
func (c *CephOss) AppendObject(ctx context.Context, req *oss.AppendObjectInput) (*oss.AppendObjectOutput, error) {
	return nil, errors.New("AppendObject method not supported on AWS")
}

func (c *CephOss) getClient() (*s3.Client, error) {
	if c.client == nil {
		return nil, utils.ErrNotInitClient
	}
	return c.client, nil
}
