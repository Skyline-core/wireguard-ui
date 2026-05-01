package handler

import (
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"golang.zx2c4.com/wireguard/wgctrl"
)

type TrafficRange string

const (
	TrafficRange24h TrafficRange = "24h"
	TrafficRange7d  TrafficRange = "7d"
	TrafficRange30d TrafficRange = "30d"
)

// trafficSampleInterval: balance RPi CPU vs responsive rates (wgctrl each tick).
const trafficSampleInterval = 4 * time.Second

// recentRateWindowSamples * trafficSampleInterval ≈ 60s rolling window for KPI "gray" line (max inst. in window).
const recentRateWindowSamples = 15

type TrafficSeriesBucketVM struct {
	RxAvgBps       float64 `json:"rx_avg_bps"`
	TxAvgBps       float64 `json:"tx_avg_bps"`
	TopPeerPubKey  string  `json:"top_peer_public_key"`
	TopPeerRxBytes int64   `json:"top_peer_rx_bytes"`
	TopPeerTxBytes int64   `json:"top_peer_tx_bytes"`
}

type TrafficSeriesResponseVM struct {
	Range          TrafficRange             `json:"range"`
	Buckets        []TrafficSeriesBucketVM `json:"buckets"`
	PeerTotals     []TrafficPeerTotalVM     `json:"peer_totals"`
	PeerCurrentTotals []TrafficPeerTotalVM  `json:"peer_current_totals"`
	MaxTotalBps    float64                  `json:"max_total_bps"`
	RxRateNowBps   float64                  `json:"rx_rate_now_bps"`
	TxRateNowBps   float64                  `json:"tx_rate_now_bps"`
	// Recent max instant rates (bytes/s) over last ~recentRateWindowSamples samples (for KPI subtitles).
	RxRateRecentMaxBps float64 `json:"rx_rate_recent_max_bps"`
	TxRateRecentMaxBps float64 `json:"tx_rate_recent_max_bps"`
	TotalRxBytes   int64                    `json:"total_rx_bytes"`
	TotalTxBytes   int64                    `json:"total_tx_bytes"`
	// PeakTxMbps is max aggregate server→peer rate (bytes/s of sum(TransmitBytes)) → peer download.
	PeakTxMbps            float64 `json:"peak_tx_mbps"`
	PeakPeerDownloadMbps  float64 `json:"peak_peer_download_mbps"` // same as PeakTxMbps; explicit name for UI
	UpdatedAgeSecs int64                    `json:"updated_age_secs"`
}

type TrafficPeerTotalVM struct {
	PublicKey string `json:"public_key"`
	RxBytes   int64  `json:"rx_bytes"`
	TxBytes   int64  `json:"tx_bytes"`
}

type trafficCache struct {
	mu sync.Mutex

	baseBucketMs   int64
	maxBaseBuckets int

	// Ring buffer indexed by idxFor(bucketID).
	baseBucketID []int64
	rxSumBps     []float64
	rxCount      []int
	txSumBps     []float64
	txCount      []int
	// txMaxBps: max instantaneous aggregate TX rate (bytes/s) seen in this bucket (peer download).
	txMaxBps []float64
	topPeerBps   []float64
	topPeerKey   []string
	peerRxBytes  []map[string]int64
	peerTxBytes  []map[string]int64

	// Last counters sample.
	prevAtMs   int64
	prevRx     int64
	prevTx     int64
	prevPeer   map[string]PeerTrafficRow
	lastRxRate float64
	lastTxRate float64
	recentRx   []float64 // ring of last instantaneous RX rates (peer upload aggregate)
	recentTx   []float64 // ring of last instantaneous TX rates (peer download aggregate)

	totalRxBytes int64
	totalTxBytes int64

	lastUpdateAtMs int64
	hasDeltaSample bool
}

var (
	trafficCacheOnce sync.Once
	trafficCacheInst *trafficCache
)

func initTrafficCache() {
	trafficCacheInst = &trafficCache{
		baseBucketMs:    int64((30 * time.Minute).Milliseconds()),
		maxBaseBuckets: 1440, // 30d @ 30min
		baseBucketID:   make([]int64, 1440),
		rxSumBps:       make([]float64, 1440),
		rxCount:        make([]int, 1440),
		txSumBps:       make([]float64, 1440),
		txCount:        make([]int, 1440),
		txMaxBps:       make([]float64, 1440),
		topPeerBps:     make([]float64, 1440),
		topPeerKey:     make([]string, 1440),
		peerRxBytes:    make([]map[string]int64, 1440),
		peerTxBytes:    make([]map[string]int64, 1440),
		prevPeer:       make(map[string]PeerTrafficRow),
	}

	// Run background refresh at a fixed cadence.
		go func() {
			ticker := time.NewTicker(trafficSampleInterval)
			defer ticker.Stop()
			// Best effort warmup: 2 samples to build deltas.
			_ = trafficCacheInst.refreshOnce()
			time.Sleep(900 * time.Millisecond)
		_ = trafficCacheInst.refreshOnce()
		for range ticker.C {
			_ = trafficCacheInst.refreshOnce()
		}
	}()
}

func ensureTrafficCache() {
	trafficCacheOnce.Do(initTrafficCache)
}

// StartTrafficSeriesCache warms the in-memory cache in the background so the first Traffic page load is fast.
func StartTrafficSeriesCache() {
	ensureTrafficCache()
	go func() {
		if trafficCacheInst == nil {
			return
		}
		trafficCacheInst.mu.Lock()
		ready := trafficCacheInst.hasDeltaSample
		trafficCacheInst.mu.Unlock()
		if ready {
			return
		}
		_ = trafficCacheInst.refreshOnce()
		time.Sleep(900 * time.Millisecond)
		_ = trafficCacheInst.refreshOnce()
	}()
}

func appendRecentWindow(buf []float64, v float64, maxN int) []float64 {
	buf = append(buf, v)
	if len(buf) > maxN {
		buf = buf[1:]
	}
	return buf
}

func maxFloatSlice(s []float64) float64 {
	var m float64
	for _, x := range s {
		if x > m {
			m = x
		}
	}
	return m
}

func (c *trafficCache) idxFor(bucketID int64) int {
	mod := bucketID % int64(c.maxBaseBuckets)
	if mod < 0 {
		mod += int64(c.maxBaseBuckets)
	}
	return int(mod)
}

func (c *trafficCache) resetBucket(idx int, bucketID int64) {
	c.baseBucketID[idx] = bucketID
	c.rxSumBps[idx] = 0
	c.rxCount[idx] = 0
	c.txSumBps[idx] = 0
	c.txCount[idx] = 0
	c.txMaxBps[idx] = 0
	c.topPeerBps[idx] = 0
	c.topPeerKey[idx] = ""
	c.peerRxBytes[idx] = nil
	c.peerTxBytes[idx] = nil
}

func (c *trafficCache) readTotalsFromKernel() (rxBytes int64, txBytes int64, peers map[string]PeerTrafficRow, err error) {
	wgClient, err := wgctrl.New()
	if err != nil {
		return 0, 0, nil, err
	}
	defer func() { _ = wgClient.Close() }()

	devices, err := wgClient.Devices()
	if err != nil {
		return 0, 0, nil, err
	}

	peers = make(map[string]PeerTrafficRow)
	for i := range devices {
		for j := range devices[i].Peers {
			rx := devices[i].Peers[j].ReceiveBytes
			tx := devices[i].Peers[j].TransmitBytes
			rxBytes += rx
			txBytes += tx
			pk := devices[i].Peers[j].PublicKey.String()
			p := peers[pk]
			p.Rx += rx
			p.Tx += tx
			peers[pk] = p
		}
	}
	return rxBytes, txBytes, peers, nil
}

// refreshOnce updates last totals and, if we have previous sample, stores delta rates into buckets.
func (c *trafficCache) refreshOnce() error {
	now := time.Now()
	nowMs := now.UnixMilli()

	totalRx, totalTx, peerTotals, err := c.readTotalsFromKernel()
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.totalRxBytes = totalRx
	c.totalTxBytes = totalTx

	if c.prevAtMs != 0 {
		dtSec := float64(nowMs-c.prevAtMs) / 1000.0
		if dtSec > 0 {
			rxRate := float64(totalRx-c.prevRx) / dtSec // bytes/s
			txRate := float64(totalTx-c.prevTx) / dtSec // bytes/s
			if rxRate < 0 {
				rxRate = 0
			}
			if txRate < 0 {
				txRate = 0
			}
			c.lastRxRate = rxRate
			c.lastTxRate = txRate
			c.recentTx = appendRecentWindow(c.recentTx, txRate, recentRateWindowSamples)
			c.recentRx = appendRecentWindow(c.recentRx, rxRate, recentRateWindowSamples)

			bid := nowMs / c.baseBucketMs
			idx := c.idxFor(bid)
			if c.baseBucketID[idx] != bid {
				c.resetBucket(idx, bid)
			}
			c.rxSumBps[idx] += rxRate
			c.rxCount[idx] += 1
			c.txSumBps[idx] += txRate
			c.txCount[idx] += 1
			if txRate > c.txMaxBps[idx] {
				c.txMaxBps[idx] = txRate
			}

			// Keep the most active peer seen for this bucket (by combined RX+TX rate).
			sampleTopPeer := ""
			sampleTopBps := 0.0
			for pk, nowPeer := range peerTotals {
				prevPeer, ok := c.prevPeer[pk]
				if !ok {
					continue
				}
				prx := float64(nowPeer.Rx-prevPeer.Rx) / dtSec
				ptx := float64(nowPeer.Tx-prevPeer.Tx) / dtSec
				if prx < 0 {
					prx = 0
				}
				if ptx < 0 {
					ptx = 0
				}
				pTotal := prx + ptx
				drx := nowPeer.Rx - prevPeer.Rx
				dtx := nowPeer.Tx - prevPeer.Tx
				if drx < 0 {
					drx = 0
				}
				if dtx < 0 {
					dtx = 0
				}
				if drx > 0 {
					if c.peerRxBytes[idx] == nil {
						c.peerRxBytes[idx] = make(map[string]int64)
					}
					c.peerRxBytes[idx][pk] += drx
				}
				if dtx > 0 {
					if c.peerTxBytes[idx] == nil {
						c.peerTxBytes[idx] = make(map[string]int64)
					}
					c.peerTxBytes[idx][pk] += dtx
				}
				if pTotal > sampleTopBps {
					sampleTopBps = pTotal
					sampleTopPeer = pk
				}
			}
			if sampleTopBps > c.topPeerBps[idx] {
				c.topPeerBps[idx] = sampleTopBps
				c.topPeerKey[idx] = sampleTopPeer
			}
			c.hasDeltaSample = true
		}
	}

	c.prevAtMs = nowMs
	c.prevRx = totalRx
	c.prevTx = totalTx
	c.prevPeer = peerTotals
	c.lastUpdateAtMs = nowMs
	return nil
}

func rangeCfg(r TrafficRange) (windowBaseBuckets int, displayCount int, groupFactor int, ok bool) {
	switch r {
	case TrafficRange24h:
		return 48, 48, 1, true
	case TrafficRange7d:
		return 336, 56, 6, true
	case TrafficRange30d:
		return 1440, 60, 24, true
	default:
		return 0, 0, 0, false
	}
}

func (c *trafficCache) buildSeries(nowMs int64, r TrafficRange) TrafficSeriesResponseVM {
	windowBaseBuckets, displayCount, groupFactor, ok := rangeCfg(r)
	if !ok {
		r = TrafficRange24h
		windowBaseBuckets, displayCount, groupFactor, _ = rangeCfg(r)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	resp := TrafficSeriesResponseVM{
		Range:           r,
		Buckets:         make([]TrafficSeriesBucketVM, 0, displayCount),
		MaxTotalBps:     1,
		RxRateNowBps:    c.lastRxRate,
		TxRateNowBps:    c.lastTxRate,
		RxRateRecentMaxBps: maxFloatSlice(c.recentRx),
		TxRateRecentMaxBps: maxFloatSlice(c.recentTx),
		TotalRxBytes:    c.totalRxBytes,
		TotalTxBytes:    c.totalTxBytes,
		PeakTxMbps:      0,
		UpdatedAgeSecs: 0,
	}

	if c.lastUpdateAtMs != 0 {
		resp.UpdatedAgeSecs = (time.Now().UnixMilli() - c.lastUpdateAtMs) / 1000
	}

	currentBid := nowMs / c.baseBucketMs
	startBid := currentBid - int64(windowBaseBuckets-1)

	maxTotal := 0.0
	peerTotals := make(map[string]TrafficPeerTotalVM)
	// Build bars.
	for i := 0; i < displayCount; i++ {
		baseStartBid := startBid + int64(i*groupFactor)
		rxSumGroup := 0.0
		rxCountGroup := 0
		txSumGroup := 0.0
		txCountGroup := 0
		groupPeerTotals := make(map[string]PeerTrafficRow)

		for g := 0; g < groupFactor; g++ {
			bid := baseStartBid + int64(g)
			idx := c.idxFor(bid)
			if c.baseBucketID[idx] != bid {
				continue
			}
			rxSumGroup += c.rxSumBps[idx]
			rxCountGroup += c.rxCount[idx]
			txSumGroup += c.txSumBps[idx]
			txCountGroup += c.txCount[idx]
			if c.peerRxBytes[idx] != nil {
				for pk, n := range c.peerRxBytes[idx] {
					gp := groupPeerTotals[pk]
					gp.Rx += n
					groupPeerTotals[pk] = gp
					p := peerTotals[pk]
					p.PublicKey = pk
					p.RxBytes += n
					peerTotals[pk] = p
				}
			}
			if c.peerTxBytes[idx] != nil {
				for pk, n := range c.peerTxBytes[idx] {
					gp := groupPeerTotals[pk]
					gp.Tx += n
					groupPeerTotals[pk] = gp
					p := peerTotals[pk]
					p.PublicKey = pk
					p.TxBytes += n
					peerTotals[pk] = p
				}
			}
		}

		rxAvg := 0.0
		if rxCountGroup > 0 {
			rxAvg = rxSumGroup / float64(rxCountGroup)
		}
		txAvg := 0.0
		if txCountGroup > 0 {
			txAvg = txSumGroup / float64(txCountGroup)
		}
		total := rxAvg + txAvg
		if total > maxTotal {
			maxTotal = total
		}
		topPeerKey := ""
		var topPeerRx, topPeerTx int64
		var topPeerTotal int64
		for pk, pt := range groupPeerTotals {
			t := pt.Rx + pt.Tx
			if t > topPeerTotal {
				topPeerTotal = t
				topPeerKey = pk
				topPeerRx = pt.Rx
				topPeerTx = pt.Tx
			}
		}

		resp.Buckets = append(resp.Buckets, TrafficSeriesBucketVM{
			RxAvgBps:       rxAvg,
			TxAvgBps:       txAvg,
			TopPeerPubKey:  topPeerKey,
			TopPeerRxBytes: topPeerRx,
			TopPeerTxBytes: topPeerTx,
		})
	}
	if maxTotal <= 0 {
		maxTotal = 1
	}
	resp.MaxTotalBps = maxTotal
	if len(peerTotals) > 0 {
		resp.PeerTotals = make([]TrafficPeerTotalVM, 0, len(peerTotals))
		for _, v := range peerTotals {
			resp.PeerTotals = append(resp.PeerTotals, v)
		}
		sort.Slice(resp.PeerTotals, func(i, j int) bool {
			li := resp.PeerTotals[i].RxBytes + resp.PeerTotals[i].TxBytes
			lj := resp.PeerTotals[j].RxBytes + resp.PeerTotals[j].TxBytes
			if li == lj {
				return resp.PeerTotals[i].PublicKey < resp.PeerTotals[j].PublicKey
			}
			return li > lj
		})
	}

	if len(c.prevPeer) > 0 {
		resp.PeerCurrentTotals = make([]TrafficPeerTotalVM, 0, len(c.prevPeer))
		for pk, p := range c.prevPeer {
			resp.PeerCurrentTotals = append(resp.PeerCurrentTotals, TrafficPeerTotalVM{
				PublicKey: pk,
				RxBytes:   p.Rx,
				TxBytes:   p.Tx,
			})
		}
		sort.Slice(resp.PeerCurrentTotals, func(i, j int) bool {
			li := resp.PeerCurrentTotals[i].RxBytes + resp.PeerCurrentTotals[i].TxBytes
			lj := resp.PeerCurrentTotals[j].RxBytes + resp.PeerCurrentTotals[j].TxBytes
			if li == lj {
				return resp.PeerCurrentTotals[i].PublicKey < resp.PeerCurrentTotals[j].PublicKey
			}
			return li > lj
		})
	}

	// Peak peer download (server TX aggregate): max over 24h window buckets.
	peakBidStart := currentBid - int64(48-1)
	peak := 0.0
	for bid := peakBidStart; bid <= currentBid; bid++ {
		idx := c.idxFor(bid)
		if c.baseBucketID[idx] != bid {
			continue
		}
		// Use max sample in bucket, not avg — short speedtests were diluted by averaging.
		peakSample := c.txMaxBps[idx]
		if peakSample <= 0 && c.txCount[idx] > 0 {
			peakSample = c.txSumBps[idx] / float64(c.txCount[idx])
		}
		mbps := (peakSample * 8) / 1e6
		if mbps > peak {
			peak = mbps
		}
	}
	// Fallback: if there are no today buckets yet, use the latest instantaneous TX rate.
	if peak <= 0 && c.lastTxRate > 0 {
		peak = (c.lastTxRate * 8) / 1e6
	}
	resp.PeakTxMbps = peak
	resp.PeakPeerDownloadMbps = peak
	return resp
}

// GetTrafficSeriesHandler returns cached traffic series for the requested range.
func GetTrafficSeries() echo.HandlerFunc {
	return func(c echo.Context) error {
		ensureTrafficCache()
		if trafficCacheInst == nil {
			return c.JSON(http.StatusInternalServerError, jsonHTTPResponse{false, "traffic cache init failed"})
		}

		rStr := c.QueryParam("range")
		var r TrafficRange
		switch rStr {
		case "7d":
			r = TrafficRange7d
		case "30d":
			r = TrafficRange30d
		default:
			r = TrafficRange24h
		}

		nowMs := time.Now().UnixMilli()

		// If not ready, do a synchronous warmup to avoid blank chart.
		trafficCacheInst.mu.Lock()
		ready := trafficCacheInst.hasDeltaSample
		trafficCacheInst.mu.Unlock()
		if !ready {
			_ = trafficCacheInst.refreshOnce()
			time.Sleep(900 * time.Millisecond)
			_ = trafficCacheInst.refreshOnce()
		}

		resp := trafficCacheInst.buildSeries(nowMs, r)
		return c.JSON(http.StatusOK, resp)
	}
}

// (Intentionally no DB dependency: this endpoint uses wgctrl + the in-memory traffic cache.)

