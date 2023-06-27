package validationtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	memcachetest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/integrations/gomemcache/memcache"
	dnstest "gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/validationtest/integrations/miekg/dns"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Integration is an interface that should be implemented by integrations (packages under the contrib/ folder) in
// order to be tested.
type Integration interface {
	// Name returns name of the integration (usually the import path starting from /contrib).
	Name() string

	// Init initializes the integration (start a server in the background, initialize the client, etc.).
	// It should return a cleanup function that will be executed after the test finishes.
	// It should also call t.Helper() before making any assertions.
	Init(t *testing.T) func()

	// GenSpans performs any operation(s) from the integration that generate spans.
	// It should call t.Helper() before making any assertions.
	GenSpans(t *testing.T)

	// NumSpans returns the number of spans that should have been generated during the test.
	NumSpans() int
}

var defaultDialer = &net.Dialer{
	Timeout:   30 * time.Second,
	KeepAlive: 30 * time.Second,
	DualStack: true,
}

func tracerEnv() string {
	// Gets the current tracer configuration variables needed for Test Agent testing and places
	// these env variables in a comma separated string of key=value pairs.
	schemaVersionStr := os.Getenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA")
	peerServiceDefaultsEnabled := false
	schemaVersion := namingschema.SchemaV1
	if v, ok := namingschema.ParseVersion(schemaVersionStr); ok {
		schemaVersion = v
		if int(v) == int(namingschema.SchemaV0) {
			peerServiceDefaultsEnabled = internal.BoolEnv("DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED", false)
		}
	}
	ddEnvVars := map[string]string{
		"DD_SERVICE":                             "Datadog-Test-Agent-Trace-Checks",
		"DD_TRACE_SPAN_ATTRIBUTE_SCHEMA":         fmt.Sprintf("v%d", schemaVersion),
		"DD_TRACE_PEER_SERVICE_DEFAULTS_ENABLED": strconv.FormatBool(peerServiceDefaultsEnabled),
	}
	values := make([]string, 0, len(ddEnvVars))
	for k, v := range ddEnvVars {
		values = append(values, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(values, ",")
}

type testAgentTransport struct {
	*http.Transport
}

func (t *testAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Adds the DD Tracer configuration environment and test session token to the trace headers
	req.Header.Add("X-Datadog-Trace-Env-Variables", tracerEnv())
	req.Header.Add("X-Datadog-Test-Session-Token", os.Getenv("CI_TEST_AGENT_SESSION_TOKEN"))
	return http.DefaultTransport.RoundTrip(req)
}

var testAgentClient = &http.Client{
	// We copy the transport to avoid using the default one, as it might be
	// augmented with tracing and we don't want these calls to be recorded.
	// See https://golang.org/pkg/net/http/#DefaultTransport .
	Transport: &testAgentTransport{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           defaultDialer.DialContext,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	},
	Timeout: 2 * time.Second,
}

func TestIntegrations(t *testing.T) {
	if _, ok := os.LookupEnv("INTEGRATION"); !ok {
		t.Skip("to enable integration test, set the INTEGRATION environment variable")
	}

	integrations := []Integration{memcachetest.New(), dnstest.New()}
	for _, ig := range integrations {
		name := ig.Name()
		t.Run(name, func(t *testing.T) {
			sessionToken := fmt.Sprintf("%s-%d", name, time.Now().Unix())
			t.Setenv("DD_SERVICE", "Datadog-Test-Agent-Trace-Checks")
			t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
			t.Setenv("CI_TEST_AGENT_SESSION_TOKEN", sessionToken)

			tracer.Start(tracer.WithAgentAddr("localhost:9126"), tracer.WithHTTPClient(testAgentClient))
			defer tracer.Stop()

			cleanup := ig.Init(t)
			defer cleanup()

			ig.GenSpans(t)

			tracer.Flush()

			assertNumSpans(t, sessionToken, ig.NumSpans())
			checkFailures(t, sessionToken)
		})
	}
}

func assertNumSpans(t *testing.T, sessionToken string, wantSpans int) {
	t.Helper()
	run := func() bool {
		req, err := http.NewRequest("GET", "http://localhost:9126/test/session/traces", nil)
		require.NoError(t, err)
		req.Header.Set("X-Datadog-Test-Session-Token", sessionToken)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)

		defer resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		var traces [][]map[string]interface{}
		require.NoError(t, json.Unmarshal(body, &traces))

		receivedSpans := 0
		for _, traceSpans := range traces {
			receivedSpans += len(traceSpans)
		}
		if receivedSpans > wantSpans {
			t.Fatalf("received more spans than expected (wantSpans: %d, receivedSpans: %d)", wantSpans, receivedSpans)
		}
		return receivedSpans == wantSpans
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	timeoutChan := time.After(5 * time.Second)

	for {
		if done := run(); done {
			return
		}
		select {
		case <-ticker.C:
			continue

		case <-timeoutChan:
			t.Fatal("timeout waiting for spans")
		}
	}
}

func checkFailures(t *testing.T, sessionToken string) {
	t.Helper()
	req, err := http.NewRequest("GET", "http://localhost:9126/test/trace_check/failures", nil)
	require.NoError(t, err)
	req.Header.Set("X-Datadog-Test-Session-Token", sessionToken)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	// the Test Agent returns a 200 if no trace-failures occurred and 400 otherwise
	if resp.StatusCode == http.StatusOK {
		return
	} else {
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Fail(t, "APM Test Agent detected failures: \n", string(body))
	}
}