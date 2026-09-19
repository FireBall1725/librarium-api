// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 FireBall1725

package providers

import (
	"context"
	"image"
	_ "image/gif" // registers the decoder for DecodeConfig
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"sync"
	"time"
)

// coverProbeLimit is how much of an image is read to find its size. A header
// sits in the first few KB; the cap stops a huge file from being pulled.
const coverProbeLimit = 256 << 10

// coverProbeTimeout bounds all probes together. A cover whose size isn't
// known in time just isn't ranked by size.
var coverProbeTimeout = 3 * time.Second

// ProbeCoverSizes reads each cover's pixel size from its header, all at once,
// so the largest can be pre-selected. It's how a 1x1 "no cover" placeholder
// stops beating a real cover just because its provider answered first.
func ProbeCoverSizes(ctx context.Context, client *http.Client, covers []CoverOption) {
	if len(covers) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, coverProbeTimeout)
	defer cancel()

	var wg sync.WaitGroup
	for i := range covers {
		wg.Add(1)
		go func(c *CoverOption) {
			defer wg.Done()
			c.Width, c.Height = probeCoverSize(ctx, client, c.CoverURL)
		}(&covers[i])
	}
	wg.Wait()
}

func probeCoverSize(ctx context.Context, client *http.Client, url string) (int, int) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, 0
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, 0
	}
	cfg, _, err := image.DecodeConfig(io.LimitReader(resp.Body, coverProbeLimit))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}
