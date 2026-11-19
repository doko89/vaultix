package http

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// Metrics is a minimal Prometheus-text-format registry (counters, gauges,
// and process info) backing /metrics.
//
// INTEGRATION NOTE (prometheus): this exists only because go.mod is frozen
// for this track. Integrator-2 adds github.com/prometheus/client_golang,
// replaces Metrics with a prometheus.Registry, and serves promhttp.Handler().
// Metric names below are chosen to survive the swap unchanged:
// manager_build_info, manager_uptime_seconds, http_requests_total.
type Metrics struct {
	mu      sync.Mutex
	started time.Time
	version string
	// counters keyed by name + sorted labels.
	counters map[string]*counter
	gauges   map[string]gauge
	log      *slog.Logger
}

type counter struct {
	name   string
	labels map[string]string
	value  uint64
}

type gauge struct {
	name   string
	labels map[string]string
	value  float64
}

// NewMetrics builds a registry. version feeds manager_build_info.
func NewMetrics(version string, log *slog.Logger) *Metrics {
	if log == nil {
		log = slog.Default()
	}
	return &Metrics{
		started:  time.Now(),
		version:  version,
		counters: map[string]*counter{},
		gauges:   map[string]gauge{},
		log:      log,
	}
}

// ObserveRequest records one served request. Paths are deliberately NOT
// labels (unbounded cardinality); method + status-class only.
func (m *Metrics) ObserveRequest(method string, code int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := "http_requests_total\x00" + method + "\x00" + fmt.Sprint(code)
	c, ok := m.counters[key]
	if !ok {
		c = &counter{name: "http_requests_total", labels: map[string]string{"method": method, "code": fmt.Sprint(code)}}
		m.counters[key] = c
	}
	c.value++
}

// SetGauge sets an absolute gauge value (e.g. queue depth reported by a
// periodic sampler).
func (m *Metrics) SetGauge(name string, value float64, labels map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gauges[gaugeKey(name, labels)] = gauge{name: name, labels: labels, value: value}
}

// IncCounter adds delta to a named counter.
func (m *Metrics) IncCounter(name string, delta uint64, labels map[string]string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := counterKey(name, labels)
	c, ok := m.counters[key]
	if !ok {
		c = &counter{name: name, labels: labels}
		m.counters[key] = c
	}
	c.value += delta
}

func sortedLabelPairs(labels map[string]string) []string {
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"\x00"+labels[k])
	}
	return out
}

func counterKey(name string, labels map[string]string) string {
	return name + "\x00" + strings.Join(sortedLabelPairs(labels), "\x01")
}

func gaugeKey(name string, labels map[string]string) string {
	return "g\x00" + counterKey(name, labels)
}

// Exposition renders the registry in Prometheus text format (version 0.0.4).
func (m *Metrics) Exposition() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	b.WriteString("# HELP manager_build_info Manager build version.\n")
	b.WriteString("# TYPE manager_build_info gauge\n")
	fmt.Fprintf(&b, "manager_build_info{version=%q} 1\n", m.version)
	b.WriteString("# HELP manager_uptime_seconds Seconds since process start.\n")
	b.WriteString("# TYPE manager_uptime_seconds counter\n")
	fmt.Fprintf(&b, "manager_uptime_seconds %d\n", int64(time.Since(m.started).Seconds()))

	names := map[string]bool{}
	for _, c := range m.counters {
		if !names["c\x00"+c.name] {
			names["c\x00"+c.name] = true
			fmt.Fprintf(&b, "# TYPE %s counter\n", c.name)
		}
		fmt.Fprintf(&b, "%s%s %d\n", c.name, formatLabels(c.labels), c.value)
	}
	seen := map[string]bool{}
	for _, g := range m.gauges {
		if !seen[g.name] {
			seen[g.name] = true
			fmt.Fprintf(&b, "# TYPE %s gauge\n", g.name)
		}
		fmt.Fprintf(&b, "%s%s %v\n", g.name, formatLabels(g.labels), g.value)
	}
	return b.String()
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+quoteLabelValue(labels[k]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func quoteLabelValue(v string) string {
	v = strings.ReplaceAll(v, "\\", `\\`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	return `"` + v + `"`
}
