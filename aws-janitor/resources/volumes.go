/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package resources

import (
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// Volumes: https://docs.aws.amazon.com/sdk-for-go/api/service/ec2/#EC2.DescribeVolumes
type Volumes struct{}

func (Volumes) MarkAndSweep(opts Options, set *Set) error {
	logger := logrus.WithField("options", opts)
	svc := ec2.New(opts.Session, aws.NewConfig().WithRegion(opts.Region))

	var toDelete []*volume // Paged call, defer deletion until we have the whole list.

	pageFunc := func(page *ec2.DescribeVolumesOutput, _ bool) bool {
		for _, vol := range page.Volumes {
			v := &volume{Account: opts.Account, Region: opts.Region, ID: *vol.VolumeId}
			tags := fromEC2Tags(vol.Tags)
			if !set.Mark(opts, v, vol.CreateTime, tags) {
				continue
			}
			// Since tags and other metadata may not propagate to volumes from their attachments,
			// we avoid deleting any volume that is currently attached. Once their attached resource is deleted,
			// we can safely delete volumes in a later clean-up run (using the mark data we saved in this run).
			if len(vol.Attachments) > 0 {
				continue
			}
			logger.Warningf("%s: deleting %T: %s (%s)", v.ARN(), vol, v.ID, tags[NameTagKey])
			if !opts.DryRun {
				toDelete = append(toDelete, v)
			}
		}
		return true
	}

	if err := svc.DescribeVolumesPages(nil, pageFunc); err != nil {
		return err
	}

	for _, vol := range toDelete {
		deleteReq := &ec2.DeleteVolumeInput{
			VolumeId: aws.String(vol.ID),
		}

		if _, err := svc.DeleteVolume(deleteReq); err != nil {
			logger.Warningf("%s: delete failed: %v", vol.ARN(), err)
		}
	}

	return nil
}

func (Volumes) ListAll(opts Options) (*Set, error) {
	svc := ec2.New(opts.Session, aws.NewConfig().WithRegion(opts.Region))
	set := NewSet(opts.DefaultTTL)
	inp := &ec2.DescribeVolumesInput{}

	err := svc.DescribeVolumesPages(inp, func(vols *ec2.DescribeVolumesOutput, _ bool) bool {
		now := time.Now()
		for _, vol := range vols.Volumes {
			arn := volume{
				Account: opts.Account,
				Region:  opts.Region,
				ID:      *vol.VolumeId,
			}.ARN()

			set.firstSeen[arn] = now
		}

		return true
	})

	return set, errors.Wrapf(err, "couldn't describe volumes for %q in %q", opts.Account, opts.Region)
}

type volume struct {
	Account string
	Region  string
	ID      string
}

func (vol volume) ARN() string {
	return fmt.Sprintf("arn:aws:ec2:%s:%s:volume/%s", vol.Region, vol.Account, vol.ID)
}

func (vol volume) ResourceKey() string {
	return vol.ARN()
}
