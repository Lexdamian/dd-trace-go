// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis // import "github.com/DataDog/dd-trace-go/v2/contrib/go-redis/redis"

import (
	"math"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
)

const defaultServiceName = "redis.client"

type clientConfig struct {
	serviceName   string
	spanName      string
	analyticsRate float64
}

// ClientOption describes options for the Redis integration.
type ClientOption interface {
	apply(*clientConfig)
}

// ClientOptionFn represents options applicable to NewClient and WrapClient.
type ClientOptionFn func(*clientConfig)

func (fn ClientOptionFn) apply(cfg *clientConfig) {
	fn(cfg)
}

func defaults(cfg *clientConfig) {
	cfg.serviceName = namingschema.ServiceNameOverrideV0(defaultServiceName, defaultServiceName)
	cfg.spanName = namingschema.OpName(namingschema.RedisOutbound)
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_REDIS_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
}

// WithService sets the given service name for the client.
func WithService(name string) ClientOptionFn {
	return func(cfg *clientConfig) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) ClientOptionFn {
	return func(cfg *clientConfig) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) ClientOptionFn {
	return func(cfg *clientConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}