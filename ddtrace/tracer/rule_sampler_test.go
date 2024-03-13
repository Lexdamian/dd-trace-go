package tracer

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRegexEqualFalseNegative(t *testing.T) {
	tests := []struct {
		name          string
		regex1        *regexp.Regexp
		regex2        *regexp.Regexp
		expectedEqual bool
	}{
		{
			name:          "nil regex equals nil regex",
			regex1:        nil,
			regex2:        nil,
			expectedEqual: true,
		},
		{
			name:          "nil regex not equal non-nil regex",
			regex1:        nil,
			regex2:        regexp.MustCompile("abc"),
			expectedEqual: false,
		},
		{
			name:          "regex with same strings",
			regex1:        regexp.MustCompile("abc.*"),
			regex2:        regexp.MustCompile("abc.*"),
			expectedEqual: true,
		},
		{
			name:          "not equal regex with wildcards",
			regex1:        regexp.MustCompile("abc.*"),
			regex2:        regexp.MustCompile("abc.*abc"),
			expectedEqual: false,
		},
		{
			name:          "same regex but false negatives",
			regex1:        regexp.MustCompile("(a+b*)*"),
			regex2:        regexp.MustCompile("(a+b)*"),
			expectedEqual: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.expectedEqual, regexEqualsFalseNegative(test.regex1, test.regex2))
		})

	}
}

func TestSamplingRuleEquals(t *testing.T) {
	tests := []struct {
		name          string
		rule1         string
		rule2         string
		expectedEqual bool
	}{
		{
			name:          "exact same rules",
			rule1:         `{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			rule2:         `{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			expectedEqual: true,
		},
		{
			name:          "different resources",
			rule1:         `{"service":"test-serv","resource":"resource-*-abc","name":"op-name","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			rule2:         `{"service":"test-serv","resource":"resource-*","name":"op-name","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			expectedEqual: false,
		},
		{
			name:          "different names",
			rule1:         `{"service":"test-serv","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			rule2:         `{"service":"test-serv","resource":"resource-*-abc","name":"op-name","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			expectedEqual: false,
		},
		{
			name:          "different tags",
			rule1:         `{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			rule2:         `{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??","tag-b":"tv-b"},"sample_rate":0.1}`,
			expectedEqual: false,
		},
		{
			name:          "different rates",
			rule1:         `{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			rule2:         `{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.2}`,
			expectedEqual: false,
		},
		{
			name:          "same rules false negatives",
			rule1:         `{"service":"test-*","resource":"resource-*","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			rule2:         `{"service":"test-*","resource":"resource-**","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}`,
			expectedEqual: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var rule_1, rule_2 SamplingRule
			assert.NoError(t, json.Unmarshal([]byte(test.rule1), &rule_1))
			assert.NoError(t, json.Unmarshal([]byte(test.rule2), &rule_2))
			assert.False(t, rule_1.Equals(nil))
			assert.Equal(t, test.expectedEqual, rule_1.Equals(&rule_2))
		})
	}
}

func TestSamplingRuleNilSlicesEqual(t *testing.T) {
	assert.True(t, Equals(nil, nil))
	{
		var rules []SamplingRule
		assert.NoError(t, json.Unmarshal([]byte(`[{"service":"abc"}]`), &rules))
		assert.False(t, Equals(nil, rules))
	}
	{
		var rules []SamplingRule
		assert.NoError(t, json.Unmarshal([]byte(`[{"service":"abc"}]`), &rules))
		assert.False(t, Equals(rules, nil))
	}
}

func TestSamplingRuleSlicesEqual(t *testing.T) {
	tests := []struct {
		name          string
		ruleset1      string
		ruleset2      string
		expectedEqual bool
	}{
		{
			name:          "empty rulesets",
			ruleset1:      "[]",
			ruleset2:      "[]",
			expectedEqual: true,
		},
		{
			name:          "one empty another not",
			ruleset1:      "[]",
			ruleset2:      `[{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}]`,
			expectedEqual: false,
		},
		{
			name:          "same rules",
			ruleset1:      `[{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}]`,
			ruleset2:      `[{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}]`,
			expectedEqual: true,
		},
		{
			name:          "different rules",
			ruleset1:      `[{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}]`,
			ruleset2:      `[{"service":"test-*","resource":"resource-*","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}]`,
			expectedEqual: false,
		},
		{
			name:     "one has extra rules",
			ruleset1: `[{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}]`,
			ruleset2: `[
				{"service":"test-*","resource":"resource-*-abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1},
				{"service":"test-*","resource":"abc","name":"op-name?","tags":{"tag-a":"tv-a??"},"sample_rate":0.1}
			]`,
			expectedEqual: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var ruleset_1, ruleset_2 []SamplingRule
			assert.NoError(t, json.Unmarshal([]byte(test.ruleset1), &ruleset_1))
			assert.NoError(t, json.Unmarshal([]byte(test.ruleset2), &ruleset_2))
			assert.Equal(t, test.expectedEqual, Equals(ruleset_1, ruleset_2))
		})
	}
}
