/*
Copyright © 2021 SUSE LLC

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

package http

import (
	"github.com/cavaliergopher/grab/v3"
	"github.com/rancher-sandbox/elemental/pkg/types/v1"
	"time"
)

type Client struct {
	client *grab.Client
}

func NewClient() *Client {
	return &Client{client: grab.NewClient()}
}

func (c Client) GetUrl(log v1.Logger, url string, destination string) error {
	req, err := grab.NewRequest(destination, url)
	if err != nil {
		log.Errorf("Failed creating a request to '%s'", url)
		return err
	}

	// start download
	log.Infof("Downloading %v...\n", req.URL())
	resp := c.client.Do(req)

	// start UI loop
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()

Loop:
	for {
		select {
		case <-t.C:
			log.Debugf("  transferred %v / %v bytes (%.2f%%)\n",
				resp.BytesComplete(),
				resp.Size,
				100*resp.Progress())

		case <-resp.Done:
			// download is complete
			break Loop
		}
	}

	// check for errors
	if err := resp.Err(); err != nil {
		log.Errorf("Download failed: %v\n", err)
		return err
	}

	log.Debugf("Download saved to ./%v \n", resp.Filename)
	return nil
}
