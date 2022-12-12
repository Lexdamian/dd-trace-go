// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package echo

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

type config struct {
	serviceName   string
	analyticsRate float64
	noDebugStack  bool
	isStatusError func(statusCode int) bool
}

// Option represents an option that can be passed to Middleware.
type Option func(*config)

func defaults(cfg *config) {
	cfg.serviceName = "echo"
	if svc := globalconfig.ServiceName(); svc != "" {
		cfg.serviceName = svc
	}
	if internal.BoolEnv("DD_TRACE_ECHO_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
	cfg.isStatusError = isServerError
}

// WithServiceName sets the given service name for the system.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return func(cfg *config) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// NoDebugStack prevents stack traces from being attached to spans finishing
// with an error. This is useful in situations where errors are frequent and
// performance is critical.
func NoDebugStack() Option {
	return func(cfg *config) {
		cfg.noDebugStack = true
	}
}

// WithStatusCheck specifies a function fn which reports whether the passed
// statusCode should be considered an error.
func WithStatusCheck(fn func(statusCode int) bool) Option {
	return func(cfg *config) {
		cfg.isStatusError = fn
	}
}

func isServerError(statusCode int) bool {
	return statusCode >= 500 && statusCode < 600
}
