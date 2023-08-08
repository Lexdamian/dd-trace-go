// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datastreams

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/version"

	"github.com/DataDog/sketches-go/ddsketch"
	"github.com/DataDog/sketches-go/ddsketch/mapping"
	"github.com/DataDog/sketches-go/ddsketch/store"
	"github.com/golang/protobuf/proto"
)

const (
	bucketDuration     = time.Second * 10
	defaultServiceName = "unnamed-go-service"
)

var sketchMapping, _ = mapping.NewLogarithmicMapping(0.01)

type statsPoint struct {
	edgeTags       []string
	hash           uint64
	parentHash     uint64
	timestamp      int64
	pathwayLatency int64
	edgeLatency    int64
}

type statsGroup struct {
	service        string
	edgeTags       []string
	hash           uint64
	parentHash     uint64
	pathwayLatency *ddsketch.DDSketch
	edgeLatency    *ddsketch.DDSketch
}

type bucket struct {
	points               map[uint64]statsGroup
	latestCommitOffsets  map[partitionConsumerKey]int64
	latestProduceOffsets map[partitionKey]int64
	start                uint64
	duration             uint64
}

func newBucket(start, duration uint64) bucket {
	return bucket{
		points:               make(map[uint64]statsGroup),
		latestCommitOffsets:  make(map[partitionConsumerKey]int64),
		latestProduceOffsets: make(map[partitionKey]int64),
		start:                start,
		duration:             duration,
	}
}

func (b bucket) export(timestampType TimestampType) StatsBucket {
	stats := make([]StatsPoint, 0, len(b.points))
	for _, s := range b.points {
		pathwayLatency, err := proto.Marshal(s.pathwayLatency.ToProto())
		if err != nil {
			log.Printf("ERROR: can't serialize pathway latency. Ignoring: %v", err)
			continue
		}
		edgeLatency, err := proto.Marshal(s.edgeLatency.ToProto())
		if err != nil {
			log.Printf("ERROR: can't serialize edge latency. Ignoring: %v", err)
			continue
		}
		stats = append(stats, StatsPoint{
			PathwayLatency: pathwayLatency,
			EdgeLatency:    edgeLatency,
			Service:        s.service,
			EdgeTags:       s.edgeTags,
			Hash:           s.hash,
			ParentHash:     s.parentHash,
			TimestampType:  timestampType,
		})
	}
	exported := StatsBucket{
		Start:    b.start,
		Duration: b.duration,
		Stats:    stats,
		Backlogs: make([]Backlog, 0, len(b.latestCommitOffsets)+len(b.latestProduceOffsets)),
	}
	for key, offset := range b.latestProduceOffsets {
		exported.Backlogs = append(exported.Backlogs, Backlog{Tags: []string{fmt.Sprintf("partition:%d", key.partition), fmt.Sprintf("topic:%s", key.topic), "type:kafka_produce"}, Value: offset})
	}
	for key, offset := range b.latestCommitOffsets {
		exported.Backlogs = append(exported.Backlogs, Backlog{Tags: []string{fmt.Sprintf("consumer_group:%s", key.group), fmt.Sprintf("partition:%d", key.partition), fmt.Sprintf("topic:%s", key.topic), "type:kafka_commit"}, Value: offset})
	}
	return exported
}

type processorStats struct {
	payloadsIn      int64
	flushedPayloads int64
	flushedBuckets  int64
	flushErrors     int64
	dropped         int64
}

type partitionKey struct {
	partition int32
	topic     string
}

type partitionConsumerKey struct {
	partition int32
	topic     string
	group     string
}

type offsetType int

const (
	produceOffset offsetType = iota
	commitOffset
)

type kafkaOffset struct {
	offset     int64
	topic      string
	group      string
	partition  int32
	offsetType offsetType
	timestamp  int64
}

type Processor struct {
	in                   chan statsPoint
	inKafka              chan kafkaOffset
	tsTypeCurrentBuckets map[int64]bucket
	tsTypeOriginBuckets  map[int64]bucket
	wg                   sync.WaitGroup
	stopped              uint64
	stop                 chan struct{} // closing this channel triggers shutdown
	flushRequest         chan chan<- struct{}
	stats                processorStats
	transport            *httpTransport
	statsd               internal.StatsdClient
	env                  string
	primaryTag           string
	service              string
	// used for tests
	timeSource func() time.Time
}

func (p *Processor) time() time.Time {
	if p.timeSource != nil {
		return p.timeSource()
	}
	return time.Now()
}

func NewProcessor(statsd internal.StatsdClient, env, service string, agentURL *url.URL, httpClient *http.Client) *Processor {
	if service == "" {
		service = defaultServiceName
	}
	return &Processor{
		tsTypeCurrentBuckets: make(map[int64]bucket),
		tsTypeOriginBuckets:  make(map[int64]bucket),
		in:                   make(chan statsPoint, 10000),
		inKafka:              make(chan kafkaOffset, 10000),
		stopped:              1,
		statsd:               statsd,
		env:                  env,
		service:              service,
		transport:            newHTTPTransport(agentURL, httpClient),
		timeSource:           time.Now,
	}
}

// alignTs returns the provided timestamp truncated to the bucket size.
// It gives us the start time of the time bucket in which such timestamp falls.
func alignTs(ts, bucketSize int64) int64 { return ts - ts%bucketSize }

func (p *Processor) getBucket(btime int64, buckets map[int64]bucket) bucket {
	b, ok := buckets[btime]
	if !ok {
		b = newBucket(uint64(btime), uint64(bucketDuration.Nanoseconds()))
		buckets[btime] = b
	}
	return b
}
func (p *Processor) addToBuckets(point statsPoint, btime int64, buckets map[int64]bucket) {
	b := p.getBucket(btime, buckets)
	group, ok := b.points[point.hash]
	if !ok {
		group = statsGroup{
			edgeTags:       point.edgeTags,
			parentHash:     point.parentHash,
			hash:           point.hash,
			pathwayLatency: ddsketch.NewDDSketch(sketchMapping, store.DenseStoreConstructor(), store.DenseStoreConstructor()),
			edgeLatency:    ddsketch.NewDDSketch(sketchMapping, store.DenseStoreConstructor(), store.DenseStoreConstructor()),
		}
		b.points[point.hash] = group
	}
	if err := group.pathwayLatency.Add(math.Max(float64(point.pathwayLatency)/float64(time.Second), 0)); err != nil {
		log.Printf("ERROR: failed to add pathway latency. Ignoring %v.", err)
	}
	if err := group.edgeLatency.Add(math.Max(float64(point.edgeLatency)/float64(time.Second), 0)); err != nil {
		log.Printf("ERROR: failed to add edge latency. Ignoring %v.", err)
	}
}

func (p *Processor) add(point statsPoint) {
	currentBucketTime := alignTs(point.timestamp, bucketDuration.Nanoseconds())
	p.addToBuckets(point, currentBucketTime, p.tsTypeCurrentBuckets)
	originTimestamp := point.timestamp - point.pathwayLatency
	originBucketTime := alignTs(originTimestamp, bucketDuration.Nanoseconds())
	p.addToBuckets(point, originBucketTime, p.tsTypeOriginBuckets)
}

func (p *Processor) addKafkaOffset(o kafkaOffset) {
	btime := alignTs(o.timestamp, bucketDuration.Nanoseconds())
	b := p.getBucket(btime, p.tsTypeCurrentBuckets)
	if o.offsetType == produceOffset {
		b.latestProduceOffsets[partitionKey{
			partition: o.partition,
			topic:     o.topic,
		}] = o.offset
		return
	}
	b.latestCommitOffsets[partitionConsumerKey{
		partition: o.partition,
		group:     o.group,
		topic:     o.topic,
	}] = o.offset
}

func (p *Processor) run(tick <-chan time.Time) {
	for {
		select {
		case s := <-p.in:
			atomic.AddInt64(&p.stats.payloadsIn, 1)
			p.add(s)
		case o := <-p.inKafka:
			p.addKafkaOffset(o)
		case now := <-tick:
			p.sendToAgent(p.flush(now))
		case done := <-p.flushRequest:
			p.sendToAgent(p.flush(time.Now().Add(bucketDuration * 10)))
			close(done)
		case <-p.stop:
			// drop in flight payloads on the input channel
			p.sendToAgent(p.flush(time.Now().Add(bucketDuration * 10)))
			return
		}
	}
}

func (p *Processor) Start() {
	if atomic.SwapUint64(&p.stopped, 0) == 0 {
		// already running
		log.Print("WARN: (*Processor).Start called more than once. This is likely p programming error.")
		return
	}
	p.stop = make(chan struct{})
	p.flushRequest = make(chan chan<- struct{})
	p.wg.Add(1)
	go p.reportStats()
	go func() {
		defer p.wg.Done()
		tick := time.NewTicker(bucketDuration)
		defer tick.Stop()
		p.run(tick.C)
	}()
}

// Flush triggers a flush and waits for it to complete.
func (p *Processor) Flush() {
	if atomic.LoadUint64(&p.stopped) > 0 {
		return
	}
	done := make(chan struct{})
	select {
	case p.flushRequest <- done:
		<-done
	case <-p.stop:
	}
}

func (p *Processor) Stop() {
	if atomic.SwapUint64(&p.stopped, 1) > 0 {
		return
	}
	close(p.stop)
	p.wg.Wait()
}

func (p *Processor) reportStats() {
	for range time.NewTicker(time.Second * 10).C {
		p.statsd.Count("datadog.datastreams.Processor.payloads_in", atomic.SwapInt64(&p.stats.payloadsIn, 0), nil, 1)
		p.statsd.Count("datadog.datastreams.Processor.flushed_payloads", atomic.SwapInt64(&p.stats.flushedPayloads, 0), nil, 1)
		p.statsd.Count("datadog.datastreams.Processor.flushed_buckets", atomic.SwapInt64(&p.stats.flushedBuckets, 0), nil, 1)
		p.statsd.Count("datadog.datastreams.Processor.flush_errors", atomic.SwapInt64(&p.stats.flushErrors, 0), nil, 1)
		p.statsd.Count("datadog.datastreams.dropped_payloads", atomic.SwapInt64(&p.stats.dropped, 0), nil, 1)
	}
}

func (p *Processor) flushBucket(buckets map[int64]bucket, bucketStart int64, timestampType TimestampType) StatsBucket {
	bucket := buckets[bucketStart]
	delete(buckets, bucketStart)
	return bucket.export(timestampType)
}

func (p *Processor) flush(now time.Time) StatsPayload {
	nowNano := now.UnixNano()
	sp := StatsPayload{
		Service:       p.service,
		Env:           p.env,
		Lang:          "go",
		TracerVersion: version.Tag,
		Stats:         make([]StatsBucket, 0, len(p.tsTypeCurrentBuckets)+len(p.tsTypeOriginBuckets)),
	}
	for ts := range p.tsTypeCurrentBuckets {
		if ts > nowNano-bucketDuration.Nanoseconds() {
			// do not flush the bucket at the current time
			continue
		}
		sp.Stats = append(sp.Stats, p.flushBucket(p.tsTypeCurrentBuckets, ts, TimestampTypeCurrent))
	}
	for ts := range p.tsTypeOriginBuckets {
		if ts > nowNano-bucketDuration.Nanoseconds() {
			// do not flush the bucket at the current time
			continue
		}
		sp.Stats = append(sp.Stats, p.flushBucket(p.tsTypeOriginBuckets, ts, TimestampTypeOrigin))
	}
	return sp
}

func (p *Processor) sendToAgent(payload StatsPayload) {
	atomic.AddInt64(&p.stats.flushedPayloads, 1)
	atomic.AddInt64(&p.stats.flushedBuckets, int64(len(payload.Stats)))
	if err := p.transport.sendPipelineStats(&payload); err != nil {
		atomic.AddInt64(&p.stats.flushErrors, 1)
	}
}

func (p *Processor) SetCheckpoint(ctx context.Context, edgeTags ...string) (Pathway, context.Context) {
	parent, hasParent := PathwayFromContext(ctx)
	parentHash := uint64(0)
	now := p.time()
	pathwayStart := now
	edgeStart := now
	if hasParent {
		pathwayStart = parent.PathwayStart()
		edgeStart = parent.EdgeStart()
		parentHash = parent.GetHash()
	}
	child := Pathway{
		hash:         pathwayHash(nodeHash(p.service, p.env, edgeTags), parentHash),
		pathwayStart: pathwayStart,
		edgeStart:    now,
	}
	select {
	case p.in <- statsPoint{
		edgeTags:       edgeTags,
		parentHash:     parentHash,
		hash:           child.hash,
		timestamp:      now.UnixNano(),
		pathwayLatency: now.Sub(pathwayStart).Nanoseconds(),
		edgeLatency:    now.Sub(edgeStart).Nanoseconds(),
	}:
	default:
		atomic.AddInt64(&p.stats.dropped, 1)
	}
	return child, ContextWithPathway(ctx, child)
}
func (p *Processor) TrackKafkaCommitOffset(group string, topic string, partition int32, offset int64) {
	select {
	case p.inKafka <- kafkaOffset{
		offset:     offset,
		group:      group,
		topic:      topic,
		partition:  partition,
		offsetType: commitOffset,
		timestamp:  p.time().UnixNano(),
	}:
	default:
		atomic.AddInt64(&p.stats.dropped, 1)
	}
}

func (p *Processor) TrackKafkaProduceOffset(topic string, partition int32, offset int64) {
	select {
	case p.inKafka <- kafkaOffset{
		offset:     offset,
		topic:      topic,
		partition:  partition,
		offsetType: produceOffset,
		timestamp:  p.time().UnixNano(),
	}:
	default:
		atomic.AddInt64(&p.stats.dropped, 1)
	}
}
