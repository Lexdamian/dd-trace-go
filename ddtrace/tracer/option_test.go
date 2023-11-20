// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
	"github.com/DataDog/dd-trace-go/v2/internal/traceprof"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withTransport(t transport) StartOption {
	return func(c *config) {
		c.transport = t
	}
}

func withTickChan(ch <-chan time.Time) StartOption {
	return func(c *config) {
		c.tickChan = ch
	}
}

// testStatsd asserts that the given statsd.Client can successfully send metrics
// to a UDP listener located at addr.
func testStatsd(t *testing.T, cfg *config, addr string) {
	client, err := newStatsdClient(cfg)
	require.NoError(t, err)
	defer client.Close()
	require.Equal(t, addr, cfg.dogstatsdAddr)
	_, err = net.ResolveUDPAddr("udp", addr)
	require.NoError(t, err)

	client.Count("name", 1, []string{"tag"}, 1)
	require.NoError(t, client.Close())
}

func TestStatsdUDPConnect(t *testing.T) {
	defer func(old string) { os.Setenv("DD_DOGSTATSD_PORT", old) }(os.Getenv("DD_DOGSTATSD_PORT"))
	os.Setenv("DD_DOGSTATSD_PORT", "8111")
	cfg, err := newConfig()
	require.NoError(t, err)
	testStatsd(t, cfg, net.JoinHostPort(defaultHostname, "8111"))
	addr := net.JoinHostPort(defaultHostname, "8111")

	client, err := newStatsdClient(cfg)
	require.NoError(t, err)
	defer client.Close()
	require.Equal(t, addr, cfg.dogstatsdAddr)
	udpaddr, err := net.ResolveUDPAddr("udp", addr)
	require.NoError(t, err)
	conn, err := net.ListenUDP("udp", udpaddr)
	require.NoError(t, err)
	defer conn.Close()

	client.Count("name", 1, []string{"tag"}, 1)
	require.NoError(t, client.Close())

	done := make(chan struct{})
	buf := make([]byte, 4096)
	n := 0
	go func() {
		n, _ = io.ReadAtLeast(conn, buf, 1)
		close(done)
	}()

	select {
	case <-done:
		// OK
	case <-time.After(1 * time.Second):
		require.Fail(t, "No data was flushed.")
	}
	assert.Contains(t, string(buf[:n]), "name:1|c|#lang:go")
}

func TestAutoDetectStatsd(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		cfg, err := newConfig()
		require.NoError(t, err)

		testStatsd(t, cfg, net.JoinHostPort(defaultHostname, "8125"))
	})

	t.Run("socket", func(t *testing.T) {
		if strings.HasPrefix(runtime.GOOS, "windows") {
			t.Skip("Unix only")
		}
		if testing.Short() {
			return
		}
		dir, err := ioutil.TempDir("", "socket")
		if err != nil {
			t.Fatal(err)
		}
		addr := filepath.Join(dir, "dsd.socket")

		defer func(old string) { defaultSocketDSD = old }(defaultSocketDSD)
		defaultSocketDSD = addr

		uaddr, err := net.ResolveUnixAddr("unixgram", addr)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := net.ListenUnixgram("unixgram", uaddr)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))

		cfg, err := newConfig()
		assert.NoError(t, err)
		statsd, err := newStatsdClient(cfg)
		require.NoError(t, err)
		defer statsd.Close()
		require.Equal(t, cfg.dogstatsdAddr, "unix://"+addr)
		statsd.Count("name", 1, []string{"tag"}, 1)

		buf := make([]byte, 17)
		n, err := conn.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		require.Contains(t, string(buf[:n]), "name:1|c|#lang:go")
	})

	t.Run("env", func(t *testing.T) {
		defer func(old string) { os.Setenv("DD_DOGSTATSD_PORT", old) }(os.Getenv("DD_DOGSTATSD_PORT"))
		os.Setenv("DD_DOGSTATSD_PORT", "8111")
		cfg, err := newConfig()
		assert.NoError(t, err)
		testStatsd(t, cfg, net.JoinHostPort(defaultHostname, "8111"))
	})

	t.Run("agent", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(`{"statsd_port":0}`))
			}))
			defer srv.Close()
			cfg, err := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")))
			assert.NoError(t, err)
			testStatsd(t, cfg, net.JoinHostPort(defaultHostname, "8125"))
		})

		t.Run("port", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte(`{"statsd_port":8999}`))
			}))
			defer srv.Close()
			cfg, err := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")))
			assert.NoError(t, err)
			testStatsd(t, cfg, net.JoinHostPort(defaultHostname, "8999"))
		})
	})
}

func TestLoadAgentFeatures(t *testing.T) {
	t.Run("zero", func(t *testing.T) {
		t.Run("disabled", func(t *testing.T) {
			cfg, err := newConfig(WithLambdaMode(true))
			assert.NoError(t, err)
			assert.Zero(t, cfg.agent)
		})

		t.Run("unreachable", func(t *testing.T) {
			if testing.Short() {
				return
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))
			defer srv.Close()
			cfg, err := newConfig(WithAgentAddr("127.9.9.9:8181"))
			assert.NoError(t, err)
			assert.Zero(t, cfg.agent)
		})

		t.Run("StatusNotFound", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			}))
			defer srv.Close()
			cfg, err := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")))
			require.NoError(t, err)
			assert.Zero(t, cfg.agent)
		})

		t.Run("error", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Write([]byte("Not JSON"))
			}))
			defer srv.Close()
			cfg, err := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")))
			require.NoError(t, err)
			assert.Zero(t, cfg.agent)
		})
	})

	t.Run("OK", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"feature_flags":["a","b"],"client_drop_p0s":true,"statsd_port":8999}`))
		}))
		defer srv.Close()
		cfg, err := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")))
		assert.NoError(t, err)
		assert.True(t, cfg.agent.DropP0s)
		assert.Equal(t, cfg.agent.StatsdPort, 8999)
		assert.EqualValues(t, cfg.agent.featureFlags, map[string]struct{}{
			"a": {},
			"b": {},
		})
		assert.True(t, cfg.agent.Stats)
		assert.True(t, cfg.agent.HasFlag("a"))
		assert.True(t, cfg.agent.HasFlag("b"))
	})

	t.Run("discovery", func(t *testing.T) {
		defer func(old string) { os.Setenv("DD_TRACE_FEATURES", old) }(os.Getenv("DD_TRACE_FEATURES"))
		os.Setenv("DD_TRACE_FEATURES", "discovery")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"endpoints":["/v0.6/stats"],"client_drop_p0s":true,"statsd_port":8999}`))
		}))
		defer srv.Close()
		cfg, err := newConfig(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")))
		assert.NoError(t, err)
		assert.True(t, cfg.agent.DropP0s)
		assert.True(t, cfg.agent.Stats)
		assert.Equal(t, 8999, cfg.agent.StatsdPort)
	})
}

// clearIntegreationsForTests clears the state of all integrations
func clearIntegrationsForTests() {
	for name, state := range contribIntegrations {
		state.imported = false
		contribIntegrations[name] = state
	}
}

func TestAgentIntegration(t *testing.T) {
	t.Run("err", func(t *testing.T) {
		assert.False(t, MarkIntegrationImported("this-integration-does-not-exist"))
	})

	// this test is run before configuring integrations and after: ensures we clean up global state
	defaultUninstrumentedTest := func(t *testing.T) {
		cfg, err := newConfig()
		assert.Nil(t, err)
		defer clearIntegrationsForTests()

		cfg.loadContribIntegrations(nil)
		assert.Equal(t, len(cfg.integrations), 54)
		for integrationName, v := range cfg.integrations {
			assert.False(t, v.Instrumented, "integrationName=%s", integrationName)
		}
	}
	t.Run("default_before", defaultUninstrumentedTest)

	t.Run("OK import", func(t *testing.T) {
		cfg, err := newConfig()
		assert.Nil(t, err)
		defer clearIntegrationsForTests()

		ok := MarkIntegrationImported("github.com/go-chi/chi")
		assert.True(t, ok)
		cfg.loadContribIntegrations([]*debug.Module{})
		assert.True(t, cfg.integrations["chi"].Instrumented)
	})

	t.Run("available", func(t *testing.T) {
		cfg, err := newConfig()
		assert.Nil(t, err)
		defer clearIntegrationsForTests()

		d := debug.Module{
			Path:    "github.com/go-redis/redis",
			Version: "v1.538",
		}

		deps := []*debug.Module{&d}
		cfg.loadContribIntegrations(deps)
		assert.True(t, cfg.integrations["Redis"].Available)
		assert.Equal(t, cfg.integrations["Redis"].Version, "v1.538")
	})

	t.Run("grpc", func(t *testing.T) {
		cfg, err := newConfig()
		assert.Nil(t, err)
		defer clearIntegrationsForTests()

		d := debug.Module{
			Path:    "google.golang.org/grpc",
			Version: "v1.520",
		}

		deps := []*debug.Module{&d}
		cfg.loadContribIntegrations(deps)
		assert.True(t, cfg.integrations["gRPC"].Available)
		assert.Equal(t, cfg.integrations["gRPC"].Version, "v1.520")
		assert.False(t, cfg.integrations["gRPC v12"].Available)
	})

	t.Run("grpc v12", func(t *testing.T) {
		cfg, err := newConfig()
		assert.Nil(t, err)
		defer clearIntegrationsForTests()

		d := debug.Module{
			Path:    "google.golang.org/grpc",
			Version: "v1.10",
		}

		deps := []*debug.Module{&d}
		cfg.loadContribIntegrations(deps)
		assert.True(t, cfg.integrations["gRPC v12"].Available)
		assert.Equal(t, cfg.integrations["gRPC v12"].Version, "v1.10")
		assert.False(t, cfg.integrations["gRPC"].Available)
	})

	t.Run("grpc bad", func(t *testing.T) {
		cfg, err := newConfig()
		assert.Nil(t, err)
		defer clearIntegrationsForTests()

		d := debug.Module{
			Path:    "google.golang.org/grpc",
			Version: "v10.10",
		}

		deps := []*debug.Module{&d}
		cfg.loadContribIntegrations(deps)
		assert.False(t, cfg.integrations["gRPC v12"].Available)
		assert.Equal(t, cfg.integrations["gRPC v12"].Version, "")
		assert.False(t, cfg.integrations["gRPC"].Available)
	})

	// ensure we clean up global state
	t.Run("default_after", defaultUninstrumentedTest)
}

type contribPkg struct {
	Dir        string
	Root       string
	ImportPath string
	Name       string
}

func TestIntegrationEnabled(t *testing.T) {
	body, err := exec.Command("go", "list", "-json", "../../contrib/...").Output()
	if err != nil {
		t.Fatalf(err.Error())
	}
	var packages []contribPkg
	stream := json.NewDecoder(strings.NewReader(string(body)))
	for stream.More() {
		var out contribPkg
		err := stream.Decode(&out)
		if err != nil {
			t.Fatalf(err.Error())
		}
		packages = append(packages, out)
	}
	for _, pkg := range packages {
		if strings.Contains(pkg.ImportPath, "/test") || strings.Contains(pkg.ImportPath, "/internal") {
			continue
		}
		p := strings.Replace(pkg.Dir, pkg.Root, "../..", 1)
		body, err := exec.Command("grep", "-rl", "MarkIntegrationImported", p).Output()
		if err != nil {
			t.Fatalf(err.Error())
		}
		assert.NotEqual(t, len(body), 0, "expected %s to call MarkIntegrationImported", pkg.Name)
	}
}

func TestTracerOptionsDefaults(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig()
		assert.NoError(err)
		assert.Equal(float64(1), c.sampler.(RateSampler).Rate())
		assert.Regexp(`tracer\.test(\.exe)?`, c.serviceName)
		assert.Equal(&url.URL{Scheme: "http", Host: "localhost:8126"}, c.agentURL)
		assert.Equal("localhost:8125", c.dogstatsdAddr)
		assert.Nil(nil, c.httpClient)
		assert.Equal(defaultClient, c.httpClient)
	})

	t.Run("http-client", func(t *testing.T) {
		c, err := newConfig()
		assert.NoError(t, err)
		assert.Equal(t, defaultClient, c.httpClient)
		client := &http.Client{}
		WithHTTPClient(client)(c)
		assert.Equal(t, client, c.httpClient)
	})

	t.Run("analytics", func(t *testing.T) {
		t.Run("option", func(t *testing.T) {
			defer globalconfig.SetAnalyticsRate(math.NaN())
			assert := assert.New(t)
			assert.True(math.IsNaN(globalconfig.AnalyticsRate()))
			tracer, err := newTracer(WithAnalyticsRate(0.5))
			defer tracer.Stop()
			assert.NoError(err)
			assert.Equal(0.5, globalconfig.AnalyticsRate())
			tracer, err = newTracer(WithAnalytics(false))
			defer tracer.Stop()
			assert.True(math.IsNaN(globalconfig.AnalyticsRate()))
			tracer, err = newTracer(WithAnalytics(true))
			defer tracer.Stop()
			assert.NoError(err)
			assert.Equal(1., globalconfig.AnalyticsRate())
		})

		t.Run("env/on", func(t *testing.T) {
			os.Setenv("DD_TRACE_ANALYTICS_ENABLED", "true")
			defer os.Unsetenv("DD_TRACE_ANALYTICS_ENABLED")
			defer globalconfig.SetAnalyticsRate(math.NaN())
			newConfig()
			assert.Equal(t, 1.0, globalconfig.AnalyticsRate())
		})

		t.Run("env/off", func(t *testing.T) {
			os.Setenv("DD_TRACE_ANALYTICS_ENABLED", "kj12")
			defer os.Unsetenv("DD_TRACE_ANALYTICS_ENABLED")
			defer globalconfig.SetAnalyticsRate(math.NaN())
			newConfig()
			assert.True(t, math.IsNaN(globalconfig.AnalyticsRate()))
		})
	})

	t.Run("dogstatsd", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			tracer, err := newTracer()
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "localhost:8125")
		})

		t.Run("env-host", func(t *testing.T) {
			os.Setenv("DD_AGENT_HOST", "localhost")
			defer os.Unsetenv("DD_AGENT_HOST")
			tracer, err := newTracer()
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "localhost:8125")
		})

		t.Run("env-port", func(t *testing.T) {
			os.Setenv("DD_DOGSTATSD_PORT", "123")
			defer os.Unsetenv("DD_DOGSTATSD_PORT")
			tracer, err := newTracer()
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "localhost:123")
		})

		t.Run("env-both", func(t *testing.T) {
			os.Setenv("DD_AGENT_HOST", "localhost")
			os.Setenv("DD_DOGSTATSD_PORT", "123")
			defer os.Unsetenv("DD_AGENT_HOST")
			defer os.Unsetenv("DD_DOGSTATSD_PORT")
			tracer, err := newTracer()
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "localhost:123")
		})

		t.Run("env-env", func(t *testing.T) {
			os.Setenv("DD_ENV", "testEnv")
			defer os.Unsetenv("DD_ENV")
			tracer, err := newTracer()
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, "testEnv", c.env)
		})

		t.Run("option", func(t *testing.T) {
			tracer, err := newTracer(WithDogstatsdAddress("10.1.0.12:4002"))
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, c.dogstatsdAddr, "10.1.0.12:4002")
		})
	})

	t.Run("env-agentAddr", func(t *testing.T) {
		os.Setenv("DD_AGENT_HOST", "localhost")
		defer os.Unsetenv("DD_AGENT_HOST")
		tracer, err := newTracer()
		defer tracer.Stop()
		assert.NoError(t, err)
		c := tracer.config
		assert.Equal(t, &url.URL{Scheme: "http", Host: "localhost:8126"}, c.agentURL)
	})

	t.Run("env-agentURL", func(t *testing.T) {
		t.Run("env", func(t *testing.T) {
			t.Setenv("DD_TRACE_AGENT_URL", "https://custom:1234")
			tracer, err := newTracer()
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "https", Host: "custom:1234"}, c.agentURL)
		})

		t.Run("override-env", func(t *testing.T) {
			t.Setenv("DD_AGENT_HOST", "localhost")
			t.Setenv("DD_TRACE_AGENT_PORT", "3333")
			t.Setenv("DD_TRACE_AGENT_URL", "https://custom:1234")
			tracer, err := newTracer()
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "https", Host: "custom:1234"}, c.agentURL)
		})

		t.Run("code-override", func(t *testing.T) {
			t.Setenv("DD_TRACE_AGENT_URL", "https://custom:1234")
			tracer, err := newTracer(WithAgentAddr("testhost:3333"))
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "http", Host: "testhost:3333"}, c.agentURL)
		})

		t.Run("code-override-full-URL", func(t *testing.T) {
			t.Setenv("DD_TRACE_AGENT_URL", "https://custom:1234")
			tracer, err := newTracer(WithAgentURL("http://testhost:3333"))
			assert.Nil(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "http", Host: "testhost:3333"}, c.agentURL)
		})

		t.Run("code-override-full-URL-error", func(t *testing.T) {
			tp := new(log.RecordLogger)
			// Have to use UseLogger directly before tracer logger is set
			defer log.UseLogger(tp)()
			t.Setenv("DD_TRACE_AGENT_URL", "https://localhost:1234")
			tracer, err := newTracer(WithAgentURL("go://testhost:3333"))
			assert.Nil(t, err)
			defer tracer.Stop()
			c := tracer.config
			assert.Equal(t, &url.URL{Scheme: "https", Host: "localhost:1234"}, c.agentURL)
			cond := func() bool {
				return strings.Contains(strings.Join(tp.Logs(), ""), "Unsupported protocol")
			}
			assert.Eventually(t, cond, 1*time.Second, 75*time.Millisecond)
		})
	})

	t.Run("override", func(t *testing.T) {
		os.Setenv("DD_ENV", "dev")
		defer os.Unsetenv("DD_ENV")
		assert := assert.New(t)
		env := "production"
		tracer, err := newTracer(WithEnv(env))
		defer tracer.Stop()
		assert.NoError(err)
		c := tracer.config
		assert.Equal(env, c.env)
	})

	t.Run("trace_enabled", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			tracer, err := newTracer()
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.True(t, c.enabled)
		})

		t.Run("override", func(t *testing.T) {
			os.Setenv("DD_TRACE_ENABLED", "false")
			defer os.Unsetenv("DD_TRACE_ENABLED")
			tracer, err := newTracer()
			defer tracer.Stop()
			assert.NoError(t, err)
			c := tracer.config
			assert.False(t, c.enabled)
		})
	})

	t.Run("other", func(t *testing.T) {
		assert := assert.New(t)
		tracer, err := newTracer(
			WithSampler(NewRateSampler(0.5)),
			WithAgentAddr("ddagent.consul.local:58126"),
			WithGlobalTag("k", "v"),
			WithDebugMode(true),
			WithEnv("testEnv"),
		)
		defer tracer.Stop()
		assert.NoError(err)
		c := tracer.config
		assert.Equal(float64(0.5), c.sampler.(RateSampler).Rate())
		assert.Equal(&url.URL{Scheme: "http", Host: "ddagent.consul.local:58126"}, c.agentURL)
		assert.NotNil(c.globalTags)
		assert.Equal("v", c.globalTags["k"])
		assert.Equal("testEnv", c.env)
		assert.True(c.debug)
	})

	t.Run("env-tags", func(t *testing.T) {
		os.Setenv("DD_TAGS", "env:test, aKey:aVal,bKey:bVal, cKey:")
		defer os.Unsetenv("DD_TAGS")

		assert := assert.New(t)
		c, err := newConfig()

		assert.NoError(err)
		assert.Equal("test", c.globalTags["env"])
		assert.Equal("aVal", c.globalTags["aKey"])
		assert.Equal("bVal", c.globalTags["bKey"])
		assert.Equal("", c.globalTags["cKey"])

		dVal, ok := c.globalTags["dKey"]
		assert.False(ok)
		assert.Equal(nil, dVal)
	})

	t.Run("profiler-endpoints", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			c, err := newConfig()
			assert.NoError(t, err)
			assert.True(t, c.profilerEndpoints)
		})

		t.Run("override", func(t *testing.T) {
			os.Setenv(traceprof.EndpointEnvVar, "false")
			defer os.Unsetenv(traceprof.EndpointEnvVar)
			c, err := newConfig()
			assert.NoError(t, err)
			assert.False(t, c.profilerEndpoints)
		})
	})

	t.Run("profiler-hotspots", func(t *testing.T) {
		t.Run("default", func(t *testing.T) {
			c, err := newConfig()
			assert.NoError(t, err)
			assert.True(t, c.profilerHotspots)
		})

		t.Run("override", func(t *testing.T) {
			os.Setenv(traceprof.CodeHotspotsEnvVar, "false")
			defer os.Unsetenv(traceprof.CodeHotspotsEnvVar)
			c, err := newConfig()
			assert.NoError(t, err)
			assert.False(t, c.profilerHotspots)
		})
	})

	t.Run("env-mapping", func(t *testing.T) {
		os.Setenv("DD_SERVICE_MAPPING", "tracer.test:test2, svc:Newsvc,http.router:myRouter, noval:")
		defer os.Unsetenv("DD_SERVICE_MAPPING")

		assert := assert.New(t)
		c, err := newConfig()

		assert.NoError(err)
		assert.Equal("test2", c.serviceMappings["tracer.test"])
		assert.Equal("Newsvc", c.serviceMappings["svc"])
		assert.Equal("myRouter", c.serviceMappings["http.router"])
		assert.Equal("", c.serviceMappings["noval"])
	})

	t.Run("datadog-tags", func(t *testing.T) {
		t.Run("can-set-value", func(t *testing.T) {
			os.Setenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", "200")
			defer os.Unsetenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH")
			assert := assert.New(t)
			c, err := newConfig()
			assert.NoError(err)
			p := c.propagator.(*chainedPropagator).injectors[1].(*propagator)
			assert.Equal(200, p.cfg.MaxTagsHeaderLen)
		})

		t.Run("default", func(t *testing.T) {
			assert := assert.New(t)
			c, err := newConfig()
			assert.NoError(err)
			p := c.propagator.(*chainedPropagator).injectors[1].(*propagator)
			assert.Equal(128, p.cfg.MaxTagsHeaderLen)
		})

		t.Run("clamped-to-zero", func(t *testing.T) {
			os.Setenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", "-520")
			defer os.Unsetenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH")
			assert := assert.New(t)
			c, err := newConfig()
			assert.NoError(err)
			p := c.propagator.(*chainedPropagator).injectors[1].(*propagator)
			assert.Equal(0, p.cfg.MaxTagsHeaderLen)
		})

		t.Run("upper-clamp", func(t *testing.T) {
			os.Setenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH", "1000")
			defer os.Unsetenv("DD_TRACE_X_DATADOG_TAGS_MAX_LENGTH")
			assert := assert.New(t)
			c, err := newConfig()
			assert.NoError(err)
			p := c.propagator.(*chainedPropagator).injectors[1].(*propagator)
			assert.Equal(512, p.cfg.MaxTagsHeaderLen)
		})
	})

	t.Run("attribute-schema", func(t *testing.T) {
		t.Run("defaults", func(t *testing.T) {
			c, err := newConfig()
			assert.NoError(t, err)
			assert.Equal(t, 0, c.spanAttributeSchemaVersion)
			assert.Equal(t, false, namingschema.UseGlobalServiceName())
		})

		t.Run("env-vars", func(t *testing.T) {
			t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
			t.Setenv("DD_TRACE_REMOVE_INTEGRATION_SERVICE_NAMES_ENABLED", "true")

			prev := namingschema.UseGlobalServiceName()
			defer namingschema.SetUseGlobalServiceName(prev)

			c, err := newConfig()
			assert.NoError(t, err)
			assert.Equal(t, 1, c.spanAttributeSchemaVersion)
			assert.Equal(t, true, namingschema.UseGlobalServiceName())
		})

		t.Run("options", func(t *testing.T) {
			prev := namingschema.UseGlobalServiceName()
			defer namingschema.SetUseGlobalServiceName(prev)

			c, err := newConfig()
			assert.NoError(t, err)
			WithGlobalServiceName(true)(c)

			assert.Equal(t, true, namingschema.UseGlobalServiceName())
		})
	})

	t.Run("peer-service", func(t *testing.T) {
		t.Run("defaults", func(t *testing.T) {
			c, err := newConfig()
			assert.NoError(t, err)
			assert.Equal(t, c.peerServiceDefaultsEnabled, false)
			assert.Empty(t, c.peerServiceMappings)
		})

		t.Run("defaults-with-schema-v1", func(t *testing.T) {
			t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
			c, err := newConfig()
			assert.NoError(t, err)
			assert.Equal(t, c.peerServiceDefaultsEnabled, true)
			assert.Empty(t, c.peerServiceMappings)
		})

		t.Run("env-vars", func(t *testing.T) {
			t.Setenv("DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED", "true")
			t.Setenv("DD_TRACE_PEER_SERVICE_MAPPING", "old:new,old2:new2")
			c, err := newConfig()
			assert.NoError(t, err)
			assert.Equal(t, c.peerServiceDefaultsEnabled, true)
			assert.Equal(t, c.peerServiceMappings, map[string]string{"old": "new", "old2": "new2"})
		})

		t.Run("options", func(t *testing.T) {
			c, err := newConfig()
			assert.NoError(t, err)
			WithPeerServiceDefaults(true)(c)
			WithPeerServiceMapping("old", "new")(c)
			WithPeerServiceMapping("old2", "new2")(c)
			assert.Equal(t, c.peerServiceDefaultsEnabled, true)
			assert.Equal(t, c.peerServiceMappings, map[string]string{"old": "new", "old2": "new2"})
		})
	})

	t.Run("debug-open-spans", func(t *testing.T) {
		t.Run("defaults", func(t *testing.T) {
			c, err := newConfig()
			assert.NoError(t, err)
			assert.Equal(t, false, c.debugAbandonedSpans)
			assert.Equal(t, time.Duration(0), c.spanTimeout)
		})

		t.Run("debug-on", func(t *testing.T) {
			t.Setenv("DD_TRACE_DEBUG_ABANDONED_SPANS", "true")
			c, err := newConfig()
			assert.NoError(t, err)
			assert.Equal(t, true, c.debugAbandonedSpans)
			assert.Equal(t, 10*time.Minute, c.spanTimeout)
		})

		t.Run("timeout-set", func(t *testing.T) {
			t.Setenv("DD_TRACE_DEBUG_ABANDONED_SPANS", "true")
			t.Setenv("DD_TRACE_ABANDONED_SPAN_TIMEOUT", fmt.Sprint(time.Minute))
			c, err := newConfig()
			assert.NoError(t, err)
			assert.Equal(t, true, c.debugAbandonedSpans)
			assert.Equal(t, time.Minute, c.spanTimeout)
		})

		t.Run("with-function", func(t *testing.T) {
			c, err := newConfig()
			assert.NoError(t, err)
			WithDebugSpansMode(time.Second)(c)
			assert.Equal(t, true, c.debugAbandonedSpans)
			assert.Equal(t, time.Second, c.spanTimeout)
		})
	})
}

func TestDefaultHTTPClient(t *testing.T) {
	t.Run("no-socket", func(t *testing.T) {
		// We care that whether clients are different, but doing a deep
		// comparison is overkill and can trigger the race detector, so
		// just compare the pointers.
		assert.Same(t, defaultHTTPClient(), defaultClient)
	})

	t.Run("socket", func(t *testing.T) {
		f, err := ioutil.TempFile("", "apm.socket")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(f.Name())
		defer func(old string) { defaultSocketAPM = old }(defaultSocketAPM)
		defaultSocketAPM = f.Name()
		assert.NotSame(t, defaultHTTPClient(), defaultClient)
	})
}

func TestDefaultDogstatsdAddr(t *testing.T) {
	t.Run("no-socket", func(t *testing.T) {
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8125")
	})

	t.Run("env", func(t *testing.T) {
		defer func(old string) { os.Setenv("DD_DOGSTATSD_PORT", old) }(os.Getenv("DD_DOGSTATSD_PORT"))
		os.Setenv("DD_DOGSTATSD_PORT", "8111")
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8111")
	})

	t.Run("env+socket", func(t *testing.T) {
		defer func(old string) { os.Setenv("DD_DOGSTATSD_PORT", old) }(os.Getenv("DD_DOGSTATSD_PORT"))
		os.Setenv("DD_DOGSTATSD_PORT", "8111")
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8111")
		f, err := ioutil.TempFile("", "dsd.socket")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(f.Name())
		defer func(old string) { defaultSocketDSD = old }(defaultSocketDSD)
		defaultSocketDSD = f.Name()
		assert.Equal(t, defaultDogstatsdAddr(), "localhost:8111")
	})

	t.Run("socket", func(t *testing.T) {
		defer func(old string) { os.Setenv("DD_AGENT_HOST", old) }(os.Getenv("DD_AGENT_HOST"))
		defer func(old string) { os.Setenv("DD_DOGSTATSD_PORT", old) }(os.Getenv("DD_DOGSTATSD_PORT"))
		os.Unsetenv("DD_AGENT_HOST")
		os.Unsetenv("DD_DOGSTATSD_PORT")
		f, err := ioutil.TempFile("", "dsd.socket")
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(f.Name())
		defer func(old string) { defaultSocketDSD = old }(defaultSocketDSD)
		defaultSocketDSD = f.Name()
		assert.Equal(t, defaultDogstatsdAddr(), "unix://"+f.Name())
	})
}

func TestServiceName(t *testing.T) {
	t.Run("WithServiceName", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		assert := assert.New(t)
		c, err := newConfig(
			WithServiceName("api-intake"),
		)

		assert.NoError(err)
		assert.Equal("api-intake", c.serviceName)
		assert.Equal("", globalconfig.ServiceName())
	})

	t.Run("WithService", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		assert := assert.New(t)
		c, err := newConfig(
			WithService("api-intake"),
		)
		assert.NoError(err)
		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("env", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		os.Setenv("DD_SERVICE", "api-intake")
		defer os.Unsetenv("DD_SERVICE")
		assert := assert.New(t)
		c, err := newConfig()

		assert.NoError(err)
		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("WithGlobalTag", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		assert := assert.New(t)
		c, err := newConfig(WithGlobalTag("service", "api-intake"))
		assert.NoError(err)
		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		defer globalconfig.SetServiceName("")
		os.Setenv("DD_TAGS", "service:api-intake")
		defer os.Unsetenv("DD_TAGS")
		assert := assert.New(t)
		c, err := newConfig()

		assert.NoError(err)
		assert.Equal("api-intake", c.serviceName)
		assert.Equal("api-intake", globalconfig.ServiceName())
	})

	t.Run("override-chain", func(t *testing.T) {
		assert := assert.New(t)
		globalconfig.SetServiceName("")
		c, err := newConfig()
		assert.NoError(err)
		assert.Equal(c.serviceName, filepath.Base(os.Args[0]))
		assert.Equal("", globalconfig.ServiceName())

		os.Setenv("DD_TAGS", "service:testService")
		defer os.Unsetenv("DD_TAGS")
		globalconfig.SetServiceName("")
		c, err = newConfig()
		assert.NoError(err)
		assert.Equal(c.serviceName, "testService")
		assert.Equal("testService", globalconfig.ServiceName())

		globalconfig.SetServiceName("")
		c, err = newConfig(WithGlobalTag("service", "testService2"))
		assert.NoError(err)
		assert.Equal(c.serviceName, "testService2")
		assert.Equal("testService2", globalconfig.ServiceName())

		os.Setenv("DD_SERVICE", "testService3")
		defer os.Unsetenv("DD_SERVICE")
		globalconfig.SetServiceName("")
		c, err = newConfig(WithGlobalTag("service", "testService2"))
		assert.Equal(c.serviceName, "testService3")
		assert.Equal("testService3", globalconfig.ServiceName())

		globalconfig.SetServiceName("")
		c, err = newConfig(WithGlobalTag("service", "testService2"), WithService("testService4"))
		assert.Equal(c.serviceName, "testService4")
		assert.Equal("testService4", globalconfig.ServiceName())
	})
}

func TestTagSeparators(t *testing.T) {
	assert := assert.New(t)

	for _, tag := range []struct {
		in  string
		out map[string]string
	}{{
		in: "env:test aKey:aVal bKey:bVal cKey:",
		out: map[string]string{
			"env":  "test",
			"aKey": "aVal",
			"bKey": "bVal",
			"cKey": "",
		},
	},
		{
			in: "env:test,aKey:aVal,bKey:bVal,cKey:",
			out: map[string]string{
				"env":  "test",
				"aKey": "aVal",
				"bKey": "bVal",
				"cKey": "",
			},
		},
		{
			in: "env:test,aKey:aVal bKey:bVal cKey:",
			out: map[string]string{
				"env":  "test",
				"aKey": "aVal bKey:bVal cKey:",
			},
		},
		{
			in: "env:test     bKey :bVal dKey: dVal cKey:",
			out: map[string]string{
				"env":  "test",
				"bKey": "",
				"dKey": "",
				"dVal": "",
				"cKey": "",
			},
		},
		{
			in: "env :test, aKey : aVal bKey:bVal cKey:",
			out: map[string]string{
				"env":  "test",
				"aKey": "aVal bKey:bVal cKey:",
			},
		},
		{
			in: "env:keyWithA:Semicolon bKey:bVal cKey",
			out: map[string]string{
				"env":  "keyWithA:Semicolon",
				"bKey": "bVal",
				"cKey": "",
			},
		},
		{
			in: "env:keyWith:  , ,   Lots:Of:Semicolons ",
			out: map[string]string{
				"env":  "keyWith:",
				"Lots": "Of:Semicolons",
			},
		},
		{
			in: "a:b,c,d",
			out: map[string]string{
				"a": "b",
				"c": "",
				"d": "",
			},
		},
		{
			in: "a,1",
			out: map[string]string{
				"a": "",
				"1": "",
			},
		},
		{
			in:  "a:b:c:d",
			out: map[string]string{"a": "b:c:d"},
		},
	} {
		t.Run("", func(t *testing.T) {
			os.Setenv("DD_TAGS", tag.in)
			defer os.Unsetenv("DD_TAGS")
			c, err := newConfig()
			assert.NoError(err)
			for key, expected := range tag.out {
				got, ok := c.globalTags[key]
				assert.True(ok, "tag not found")
				assert.Equal(expected, got)
			}
		})
	}
}

func TestVersionConfig(t *testing.T) {
	t.Run("WithServiceVersion", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig(
			WithServiceVersion("1.2.3"),
		)
		assert.NoError(err)
		assert.Equal("1.2.3", c.version)
	})

	t.Run("env", func(t *testing.T) {
		os.Setenv("DD_VERSION", "1.2.3")
		defer os.Unsetenv("DD_VERSION")
		assert := assert.New(t)
		c, err := newConfig()

		assert.NoError(err)
		assert.Equal("1.2.3", c.version)
	})

	t.Run("WithGlobalTag", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig(WithGlobalTag("version", "1.2.3"))
		assert.NoError(err)
		assert.Equal("1.2.3", c.version)
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		os.Setenv("DD_TAGS", "version:1.2.3")
		defer os.Unsetenv("DD_TAGS")
		assert := assert.New(t)
		c, err := newConfig()

		assert.NoError(err)
		assert.Equal("1.2.3", c.version)
	})

	t.Run("override-chain", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig()
		assert.NoError(err)
		assert.Equal(c.version, "")

		os.Setenv("DD_TAGS", "version:1.1.1")
		defer os.Unsetenv("DD_TAGS")
		c, err = newConfig()
		assert.NoError(err)
		assert.Equal("1.1.1", c.version)

		c, err = newConfig(WithGlobalTag("version", "1.1.2"))
		assert.NoError(err)
		assert.Equal("1.1.2", c.version)

		os.Setenv("DD_VERSION", "1.1.3")
		defer os.Unsetenv("DD_VERSION")
		c, err = newConfig(WithGlobalTag("version", "1.1.2"))
		assert.NoError(err)
		assert.Equal("1.1.3", c.version)

		c, err = newConfig(WithGlobalTag("version", "1.1.2"), WithServiceVersion("1.1.4"))
		assert.NoError(err)
		assert.Equal("1.1.4", c.version)
	})
}

func TestEnvConfig(t *testing.T) {
	t.Run("WithEnv", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig(
			WithEnv("testing"),
		)
		assert.NoError(err)
		assert.Equal("testing", c.env)
	})

	t.Run("env", func(t *testing.T) {
		os.Setenv("DD_ENV", "testing")
		defer os.Unsetenv("DD_ENV")
		assert := assert.New(t)
		c, err := newConfig()

		assert.NoError(err)
		assert.Equal("testing", c.env)
	})

	t.Run("WithGlobalTag", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig(WithGlobalTag("env", "testing"))
		assert.NoError(err)
		assert.Equal("testing", c.env)
	})

	t.Run("DD_TAGS", func(t *testing.T) {
		os.Setenv("DD_TAGS", "env:testing")
		defer os.Unsetenv("DD_TAGS")
		assert := assert.New(t)
		c, err := newConfig()

		assert.NoError(err)
		assert.Equal("testing", c.env)
	})

	t.Run("override-chain", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig()
		assert.NoError(err)
		assert.Equal(c.env, "")

		os.Setenv("DD_TAGS", "env:testing1")
		defer os.Unsetenv("DD_TAGS")
		c, err = newConfig()
		assert.NoError(err)
		assert.Equal("testing1", c.env)

		c, err = newConfig(WithGlobalTag("env", "testing2"))
		assert.NoError(err)
		assert.Equal("testing2", c.env)

		os.Setenv("DD_ENV", "testing3")
		defer os.Unsetenv("DD_ENV")
		c, err = newConfig(WithGlobalTag("env", "testing2"))
		assert.NoError(err)
		assert.Equal("testing3", c.env)

		c, err = newConfig(WithGlobalTag("env", "testing2"), WithEnv("testing4"))
		assert.NoError(err)
		assert.Equal("testing4", c.env)
	})
}

func TestStatsTags(t *testing.T) {
	assert := assert.New(t)
	c, err := newConfig(WithService("serviceName"), WithEnv("envName"))
	assert.NoError(err)
	defer globalconfig.SetServiceName("")
	c.hostname = "hostName"
	tags := statsTags(c)

	assert.Contains(tags, "service:serviceName")
	assert.Contains(tags, "env:envName")
	assert.Contains(tags, "host:hostName")
}

func TestGlobalTag(t *testing.T) {
	var c config
	WithGlobalTag("k", "v")(&c)
	assert.Contains(t, statsTags(&c), "k:v")
}

func TestWithHostname(t *testing.T) {
	t.Run("WithHostname", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig(WithHostname("hostname"))
		assert.NoError(err)
		assert.Equal("hostname", c.hostname)
	})

	t.Run("env", func(t *testing.T) {
		assert := assert.New(t)
		os.Setenv("DD_TRACE_SOURCE_HOSTNAME", "hostname-env")
		defer os.Unsetenv("DD_TRACE_SOURCE_HOSTNAME")
		c, err := newConfig()
		assert.NoError(err)
		assert.Equal("hostname-env", c.hostname)
	})

	t.Run("env-override", func(t *testing.T) {
		assert := assert.New(t)

		os.Setenv("DD_TRACE_SOURCE_HOSTNAME", "hostname-env")
		defer os.Unsetenv("DD_TRACE_SOURCE_HOSTNAME")
		c, err := newConfig(WithHostname("hostname-middleware"))
		assert.NoError(err)
		assert.Equal("hostname-middleware", c.hostname)
	})
}

func TestWithTraceEnabled(t *testing.T) {
	t.Run("WithTraceEnabled", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig(WithTraceEnabled(false))
		assert.NoError(err)
		assert.False(c.enabled)
	})

	t.Run("env", func(t *testing.T) {
		assert := assert.New(t)
		os.Setenv("DD_TRACE_ENABLED", "false")
		defer os.Unsetenv("DD_TRACE_ENABLED")
		c, err := newConfig()
		assert.NoError(err)
		assert.False(c.enabled)
	})

	t.Run("env-override", func(t *testing.T) {
		assert := assert.New(t)
		os.Setenv("DD_TRACE_ENABLED", "false")
		defer os.Unsetenv("DD_TRACE_ENABLED")
		c, err := newConfig(WithTraceEnabled(true))
		assert.NoError(err)
		assert.True(c.enabled)
	})
}

func TestWithLogStartup(t *testing.T) {
	c, err := newConfig()
	assert.NoError(t, err)
	assert.True(t, c.logStartup)
	WithLogStartup(false)(c)
	assert.False(t, c.logStartup)
	WithLogStartup(true)(c)
	assert.True(t, c.logStartup)
}

func TestWithHeaderTags(t *testing.T) {
	t.Run("default-off", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		newConfig()
		assert.Equal(0, globalconfig.HeaderTagsLen())
	})

	t.Run("single-header", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		header := "Header"
		newConfig(WithHeaderTags([]string{header}))
		assert.Equal("http.request.headers.header", globalconfig.HeaderTag(header))
	})

	t.Run("header-and-tag", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		header := "Header"
		tag := "tag"
		newConfig(WithHeaderTags([]string{header + ":" + tag}))
		assert.Equal("tag", globalconfig.HeaderTag(header))
	})

	t.Run("multi-header", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		newConfig(WithHeaderTags([]string{"1header:1tag", "2header", "3header:3tag"}))
		assert.Equal("1tag", globalconfig.HeaderTag("1header"))
		assert.Equal("http.request.headers.2header", globalconfig.HeaderTag("2header"))
		assert.Equal("3tag", globalconfig.HeaderTag("3header"))
	})

	t.Run("normalization", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		newConfig(WithHeaderTags([]string{"  h!e@a-d.e*r  ", "  2header:t!a@g.  "}))
		assert.Equal(ext.HTTPRequestHeaders+".h_e_a-d_e_r", globalconfig.HeaderTag("h!e@a-d.e*r"))
		assert.Equal("t!a@g.", globalconfig.HeaderTag("2header"))
	})

	t.Run("envvar-only", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		os.Setenv("DD_TRACE_HEADER_TAGS", "  1header:1tag,2.h.e.a.d.e.r  ")
		defer os.Unsetenv("DD_TRACE_HEADER_TAGS")

		assert := assert.New(t)
		newConfig()

		assert.Equal("1tag", globalconfig.HeaderTag("1header"))
		assert.Equal(ext.HTTPRequestHeaders+".2_h_e_a_d_e_r", globalconfig.HeaderTag("2.h.e.a.d.e.r"))
	})

	t.Run("env-override", func(t *testing.T) {
		defer globalconfig.ClearHeaderTags()
		assert := assert.New(t)
		os.Setenv("DD_TRACE_HEADER_TAGS", "unexpected")
		defer os.Unsetenv("DD_TRACE_HEADER_TAGS")
		newConfig(WithHeaderTags([]string{"expected"}))
		assert.Equal(ext.HTTPRequestHeaders+".expected", globalconfig.HeaderTag("Expected"))
		assert.Equal(1, globalconfig.HeaderTagsLen())
	})

	// ensures we cleaned up global state correctly
	assert.Equal(t, 0, globalconfig.HeaderTagsLen())
}

func TestHostnameDisabled(t *testing.T) {
	t.Run("DisabledWithUDS", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_URL", "unix://somefakesocket")
		c, err := newConfig()
		assert.NoError(t, err)
		assert.False(t, c.enableHostnameDetection)
	})
	t.Run("Default", func(t *testing.T) {
		c, err := newConfig()
		assert.NoError(t, err)
		assert.True(t, c.enableHostnameDetection)
	})
	t.Run("DisableViaEnv", func(t *testing.T) {
		t.Setenv("DD_CLIENT_HOSTNAME_ENABLED", "false")
		c, err := newConfig()
		assert.NoError(t, err)
		assert.False(t, c.enableHostnameDetection)
	})
}

func TestPartialFlushing(t *testing.T) {
	t.Run("None", func(t *testing.T) {
		c, err := newConfig()
		assert.NoError(t, err)
		assert.False(t, c.partialFlushEnabled)
		assert.Equal(t, partialFlushMinSpansDefault, c.partialFlushMinSpans)
	})
	t.Run("Disabled-DefaultMinSpans", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "false")
		c, err := newConfig()
		assert.NoError(t, err)
		assert.False(t, c.partialFlushEnabled)
		assert.Equal(t, partialFlushMinSpansDefault, c.partialFlushMinSpans)
	})
	t.Run("Default-SetMinSpans", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "10")
		c, err := newConfig()
		assert.NoError(t, err)
		assert.False(t, c.partialFlushEnabled)
		assert.Equal(t, 10, c.partialFlushMinSpans)
	})
	t.Run("Enabled-DefaultMinSpans", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
		c, err := newConfig()
		assert.NoError(t, err)
		assert.True(t, c.partialFlushEnabled)
		assert.Equal(t, partialFlushMinSpansDefault, c.partialFlushMinSpans)
	})
	t.Run("Enabled-SetMinSpans", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "10")
		c, err := newConfig()
		assert.NoError(t, err)
		assert.True(t, c.partialFlushEnabled)
		assert.Equal(t, 10, c.partialFlushMinSpans)
	})
	t.Run("Enabled-SetMinSpansNegative", func(t *testing.T) {
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
		t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "-1")
		c, err := newConfig()
		assert.NoError(t, err)
		assert.True(t, c.partialFlushEnabled)
		assert.Equal(t, partialFlushMinSpansDefault, c.partialFlushMinSpans)
	})
	t.Run("WithPartialFlushOption", func(t *testing.T) {
		c, err := newConfig()
		assert.NoError(t, err)
		WithPartialFlushing(20)(c)
		assert.True(t, c.partialFlushEnabled)
		assert.Equal(t, 20, c.partialFlushMinSpans)
	})
}

func TestWithStatsComputation(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig()
		assert.NoError(err)
		assert.False(c.statsComputationEnabled)
	})
	t.Run("enabled-via-option", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig(WithStatsComputation(true))
		assert.NoError(err)
		assert.True(c.statsComputationEnabled)
	})
	t.Run("disabled-via-option", func(t *testing.T) {
		assert := assert.New(t)
		c, err := newConfig(WithStatsComputation(false))
		assert.NoError(err)
		assert.False(c.statsComputationEnabled)
	})
	t.Run("enabled-via-env", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_STATS_COMPUTATION_ENABLED", "true")
		c, err := newConfig()
		assert.NoError(err)
		assert.True(c.statsComputationEnabled)
	})
	t.Run("env-override", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_STATS_COMPUTATION_ENABLED", "false")
		c, err := newConfig(WithStatsComputation(true))
		assert.NoError(err)
		assert.True(c.statsComputationEnabled)
	})
}

func TestWithStartSpanConfig(t *testing.T) {
	var (
		assert  = assert.New(t)
		service = "service"
		parent  = newSpan("", service, "", 0, 1, 2)
		spanID  = uint64(123)
		tm, _   = time.Parse(time.RFC3339, "2019-01-01T00:00:00Z")
	)
	cfg := ddtrace.NewStartSpanConfig(
		ChildOf(parent.Context()),
		Measured(),
		ResourceName("resource"),
		ServiceName(service),
		SpanType(ext.SpanTypeWeb),
		StartTime(tm),
		Tag("key", "value"),
		WithSpanID(spanID),
		withContext(context.Background()),
	)
	// It's difficult to test the context was used to initialize the span
	// in a meaningful way, so we just check it was set in the SpanConfig.
	assert.Equal(cfg.Context, cfg.Context)

	tracer, err := newTracer()
	defer tracer.Stop()
	assert.NoError(err)

	s := tracer.StartSpan("test", WithStartSpanConfig(cfg)).(*span)
	defer s.Finish()
	assert.Equal(float64(1), s.Metrics[keyMeasured])
	assert.Equal("value", s.Meta["key"])
	assert.Equal(parent.Context().SpanID(), s.ParentID)
	assert.Equal(parent.Context().TraceID(), s.TraceID)
	assert.Equal("resource", s.Resource)
	assert.Equal(service, s.Service)
	assert.Equal(spanID, s.SpanID)
	assert.Equal(ext.SpanTypeWeb, s.Type)
	assert.Equal(tm.UnixNano(), s.Start)
}

func TestWithStartSpanConfigNonEmptyTags(t *testing.T) {
	var (
		assert = assert.New(t)
	)
	cfg := ddtrace.NewStartSpanConfig(
		Tag("key", "value"),
		Tag("k2", "shouldnt_override"),
	)

	tracer, err := newTracer()
	defer tracer.Stop()
	assert.NoError(err)

	s := tracer.StartSpan(
		"test",
		Tag("k2", "v2"),
		WithStartSpanConfig(cfg),
	).(*span)
	defer s.Finish()
	assert.Equal("v2", s.Meta["k2"])
	assert.Equal("value", s.Meta["key"])
}

func optsTestConsumer(opts ...StartSpanOption) {
	var cfg ddtrace.StartSpanConfig
	for _, o := range opts {
		o(&cfg)
	}
}

func BenchmarkConfig(b *testing.B) {
	b.Run("scenario=none", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			optsTestConsumer(
				ServiceName("SomeService"),
				ResourceName("SomeResource"),
				Tag(ext.HTTPRoute, "/some/route/?"),
			)
		}
	})
	b.Run("scenario=WithStartSpanConfig", func(b *testing.B) {
		b.ReportAllocs()
		cfg := ddtrace.NewStartSpanConfig(
			ServiceName("SomeService"),
			ResourceName("SomeResource"),
		)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			optsTestConsumer(
				WithStartSpanConfig(cfg),
				Tag(ext.HTTPRoute, "/some/route/?"),
			)
		}
	})
}

func BenchmarkStartSpanConfig(b *testing.B) {
	b.Run("scenario=none", func(b *testing.B) {
		tracer, err := newTracer()
		defer tracer.Stop()
		assert.NoError(b, err)
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tracer.StartSpan("test",
				ServiceName("SomeService"),
				ResourceName("SomeResource"),
				Tag(ext.HTTPRoute, "/some/route/?"),
			)

		}
	})
	b.Run("scenario=WithStartSpanConfig", func(b *testing.B) {
		tracer, err := newTracer()
		defer tracer.Stop()
		assert.NoError(b, err)
		b.ReportAllocs()
		cfg := ddtrace.NewStartSpanConfig(
			ServiceName("SomeService"),
			ResourceName("SomeResource"),
		)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tracer.StartSpan("test",
				WithStartSpanConfig(cfg),
				Tag(ext.HTTPRoute, "/some/route/?"),
			)
		}
	})
}
