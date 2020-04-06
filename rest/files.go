package rest

import (
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/pydio/cells-sdk-go/client/tree_service"
	"github.com/pydio/cells-sdk-go/models"
	awstransport "github.com/pydio/cells-sdk-go/transport/aws"
	"github.com/pydio/cells-sdk-go/transport/oidc"

	"github.com/pydio/cells-client/common"
)

func GetS3Client() (*s3.S3, string, error) {
	DefaultConfig.CustomHeaders = map[string]string{"User-Agent": "cells-client/" + common.Version}
	if err := ConfigFromKeyring(DefaultConfig); err != nil {
		return nil, "", err
	}
	s3Config := getS3ConfigFromSdkConfig(*DefaultConfig)
	bucketName := s3Config.Bucket
	s3Client, e := awstransport.GetS3CLient(DefaultConfig, &s3Config)
	if e != nil {
		return nil, "", e
	}
	s3Client.Config.S3DisableContentMD5Validation = aws.Bool(true)
	return s3Client, bucketName, e
}

func GetFile(pathToFile string) (io.Reader, int, error) {

	s3Client, bucketName, e := GetS3Client()
	if e != nil {
		return nil, 0, e
	}
	hO, err := s3Client.HeadObject((&s3.HeadObjectInput{}).
		SetBucket(bucketName).
		SetKey(pathToFile),
	)
	if err != nil {
		return nil, 0, err
	}
	size := int(*hO.ContentLength)

	obj, err := s3Client.GetObject((&s3.GetObjectInput{}).
		SetBucket(bucketName).
		SetKey(pathToFile),
	)
	if err != nil {
		return nil, 0, err
	}
	return obj.Body, size, nil
}

func PutFile(pathToFile string, content io.ReadSeeker, checkExists bool, errChan ...chan error) (*s3.PutObjectOutput, error) {
	s3Client, bucketName, e := GetS3Client()
	if e != nil {
		return nil, e
	}

	key := pathToFile
	var obj *s3.PutObjectOutput
	e = RetryCallback(func() error {
		var err error
		obj, err = s3Client.PutObject((&s3.PutObjectInput{}).
			SetBucket(bucketName).
			SetKey(key).
			SetBody(content),
		)
		if err != nil {
			if len(errChan) > 0 {
				errChan[0] <- err
			} else {
				fmt.Println(" ## Trying to Put file:", key)
			}
		}
		return err
	}, 3, 2*time.Second)
	if e != nil {
		return nil, fmt.Errorf("could not put object in bucket %s with key %s, \ncause: %s", bucketName, key, e.Error())
	}

	if checkExists {
		fmt.Println(" ## Waiting for file to be indexed...")
		// Now stat Node to make sure it is indexed
		e = RetryCallback(func() error {
			_, ok := StatNode(pathToFile)
			if !ok {
				return fmt.Errorf("cannot stat node just after PutFile operation")
			}
			return nil

		}, 3, 3*time.Second)
		if e != nil {
			return nil, e
		}
		fmt.Println(" ## File correctly indexed")
	}
	return obj, nil
}

func multiPartUpload(path string, content io.ReadSeeker, size int64, errChan chan error) error {

	s3Client, bucket, err := GetS3Client()
	if err != nil {
		errChan <- err
		return err
	}
	// This his now handled inside the GetS3Client function
	// s3Client.Config.S3DisableContentMD5Validation = aws.Bool(true)

	multipartOutput, err := s3Client.CreateMultipartUpload(&s3.CreateMultipartUploadInput{
		Bucket:      aws.String(bucket),
		Key:         aws.String(path),
		ContentType: aws.String("application/octet-stream"),
	})
	if err != nil {
		errChan <- err
		return err
	}

	var curr, partLength, partNumber int64
	var remaining = size
	var completedParts []*s3.CompletedPart
	partLength = 50 * 1024 * 1024

	for curr = 0; remaining != 0; curr += partLength {
		if remaining < partLength {
			partLength = remaining
		}
		// TODO refresh S3Client if required
		if ok := RefreshAndStoreIfRequired(DefaultConfig); ok {
			s3Client, _, _ = GetS3Client()
			// This his now handled inside the GetS3Client function
			// s3Client.Config.S3DisableContentMD5Validation = aws.Bool(true)
		}
		partNumber++

		pr := &partReader{
			ReadSeeker: content,
			partLength: partLength,
		}
		part, err := s3Client.UploadPart(&s3.UploadPartInput{
			Body:          aws.ReadSeekCloser(pr),
			ContentLength: aws.Int64(partLength),
			Bucket:        multipartOutput.Bucket,
			Key:           multipartOutput.Key,
			UploadId:      multipartOutput.UploadId,
			PartNumber:    aws.Int64(partNumber),
		})
		if err != nil {
			if _, err = s3Client.AbortMultipartUpload(&s3.AbortMultipartUploadInput{Bucket: multipartOutput.Bucket, Key: multipartOutput.Key, UploadId: multipartOutput.UploadId}); err != nil {
				errChan <- err
				return err
			}
			errChan <- err
			return err
		}
		completedParts = append(completedParts, &s3.CompletedPart{ETag: part.ETag, PartNumber: aws.Int64(partNumber)})
		remaining -= partLength
	}

	_, err = s3Client.CompleteMultipartUpload(&s3.CompleteMultipartUploadInput{
		Bucket:          multipartOutput.Bucket,
		Key:             multipartOutput.Key,
		UploadId:        multipartOutput.UploadId,
		MultipartUpload: &s3.CompletedMultipartUpload{Parts: completedParts},
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			default:
				errChan <- err
				return aerr
			}
		}
		errChan <- err
		return err.(awserr.Error)
	}
	return nil
}

type partReader struct {
	io.ReadSeeker
	partLength int64
	cur        int64
}

func (pr *partReader) Read(p []byte) (n int, err error) {
	targetCurs := pr.cur + int64(len(p))
	if targetCurs > pr.partLength {

		remaining := targetCurs - pr.partLength
		p2 := make([]byte, remaining)
		n, err = pr.ReadSeeker.Read(p2)
		if err != nil {
			return
		}
		copy(p, p2)
		err = io.EOF
	} else {
		n, err = pr.ReadSeeker.Read(p)
	}
	pr.cur += int64(n)
	return
}
func StatNode(pathToFile string) (*models.TreeNode, bool) {

	ctx, client, e := GetApiClient()
	if e != nil {
		return nil, false
	}
	params := &tree_service.HeadNodeParams{}
	params.SetNode(pathToFile)
	params.SetContext(ctx)
	resp, err := client.TreeService.HeadNode(params)
	if err == nil && resp.Payload.Node != nil {
		return resp.Payload.Node, true
	} else {
		return nil, false
	}

}

func ListNodesPath(path string) ([]string, error) {
	_, client, err := GetApiClient()
	if err != nil {
		return nil, err
	}
	params := tree_service.NewBulkStatNodesParams()
	params.Body = &models.RestGetBulkMetaRequest{
		Limit:     100,
		NodePaths: []string{path},
	}
	res, e := client.TreeService.BulkStatNodes(params)
	if e != nil {
		return nil, e
	}
	var nodes []string
	if len(res.Payload.Nodes) < 0 {
		return nil, nil
	}
	for _, node := range res.Payload.Nodes {
		nodes = append(nodes, node.Path)
	}
	return nodes, nil
}

func DeleteNode(paths []string) (jobUUIDs []string, e error) {
	if len(paths) < 0 {
		e = fmt.Errorf("no paths found to delete")
		return
	}
	_, client, err := GetApiClient()
	if err != nil {
		e = err
		return
	}
	var nn []*models.TreeNode
	for _, p := range paths {
		nn = append(nn, &models.TreeNode{Path: p})
	}

	params := tree_service.NewDeleteNodesParams()
	params.Body = &models.RestDeleteNodesRequest{
		Nodes: nn,
	}
	res, err := client.TreeService.DeleteNodes(params)
	if err != nil {
		e = err
		return
	}

	for _, job := range res.Payload.DeleteJobs {
		jobUUIDs = append(jobUUIDs, job.UUID)
	}
	return
}

func GetBulkMetaNode(path string) ([]*models.TreeNode, error) {
	_, client, err := GetApiClient()
	if err != nil {
		return nil, err
	}
	params := tree_service.NewBulkStatNodesParams()
	params.Body = &models.RestGetBulkMetaRequest{
		Limit:     100,
		NodePaths: []string{path},
	}
	res, e := client.TreeService.BulkStatNodes(params)
	if e != nil {
		return nil, err
	}
	return res.Payload.Nodes, nil
}

func TreeCreateNodes(nodes []*models.TreeNode) error {
	_, client, err := GetApiClient()
	if err != nil {
		return err

	}
	params := tree_service.NewCreateNodesParams()
	params.Body = &models.RestCreateNodesRequest{
		Nodes:     nodes,
		Recursive: false,
	}

	_, e := client.TreeService.CreateNodes(params)
	if e != nil {
		return e
	}
	// TODO monitor jobs to wait for the index
	return nil
}

func uploadManager(path string, content io.ReadSeeker, checkExists bool, errChan ...chan error) error {
	s3Client, bucketName, err := GetS3Client()
	if err != nil {
		return err
	}

	sess, err := session.NewSession(&s3Client.Config)
	if err != nil {
		return err
	}

	sess.Config.S3DisableContentMD5Validation = aws.Bool(true)
	uploader := s3manager.NewUploader(sess, func(u *s3manager.Uploader) {
		u.PartSize = 5 * 1024 * 1024
		u.RequestOptions = []request.Option{func(r *request.Request) {
			if ok := RefreshAndStoreIfRequired(DefaultConfig); ok {
				//fmt.Println("REFRESHED\n")
			}
			s3Config := getS3ConfigFromSdkConfig(*DefaultConfig)
			apiKey, _ := oidc.RetrieveToken(DefaultConfig)
			r.Config.WithCredentials(credentials.NewStaticCredentials(apiKey, s3Config.ApiSecret, ""))
		}}
	})

	input := &s3manager.UploadInput{
		Body:   aws.ReadSeekCloser(content),
		Bucket: aws.String(bucketName),
		Key:    aws.String(path),
	}

	if _, err := uploader.Upload(input); err != nil {
		return err
	}

	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			errChan[0] <- aerr
			return aerr
		}
		errChan[0] <- err
		return err
	}
	return nil
}
