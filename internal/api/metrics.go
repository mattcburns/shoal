package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
)

// Process-wide counters (stdlib only; Prometheus text exposition).
var (
	metricHTTPRequests atomic.Uint64
	metricJobsStarted  atomic.Uint64
	metricJobsCancel   atomic.Uint64
)

// MetricsSnapshot is a point-in-time view for tests.
type MetricsSnapshot struct {
	HTTPRequests uint64
	JobsStarted  uint64
	JobsCancel   uint64
}

// SnapshotMetrics returns current counter values.
func SnapshotMetrics() MetricsSnapshot {
	return MetricsSnapshot{
		HTTPRequests: metricHTTPRequests.Load(),
		JobsStarted:  metricJobsStarted.Load(),
		JobsCancel:   metricJobsCancel.Load(),
	}
}

// ResetMetricsForTest zeroes counters (tests only).
func ResetMetricsForTest() {
	metricHTTPRequests.Store(0)
	metricJobsStarted.Store(0)
	metricJobsCancel.Store(0)
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	var b strings.Builder
	b.WriteString("# HELP shoal_http_requests_total Total HTTP requests handled by the API server.\n")
	b.WriteString("# TYPE shoal_http_requests_total counter\n")
	fmt.Fprintf(&b, "shoal_http_requests_total %d\n", metricHTTPRequests.Load())
	b.WriteString("# HELP shoal_jobs_started_total Provisioning jobs successfully accepted via POST /v1/jobs.\n")
	b.WriteString("# TYPE shoal_jobs_started_total counter\n")
	fmt.Fprintf(&b, "shoal_jobs_started_total %d\n", metricJobsStarted.Load())
	b.WriteString("# HELP shoal_jobs_cancel_total Job cancel requests accepted via POST /v1/jobs/{id}/cancel.\n")
	b.WriteString("# TYPE shoal_jobs_cancel_total counter\n")
	fmt.Fprintf(&b, "shoal_jobs_cancel_total %d\n", metricJobsCancel.Load())
	_, _ = w.Write([]byte(b.String()))
}

type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.code = code
	r.ResponseWriter.WriteHeader(code)
}

func withHTTPMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rec, r)
		metricHTTPRequests.Add(1)
		// Touch path/code so labels can be added later without changing the metric name.
		_ = r.Method
		_ = strconv.Itoa(rec.code)
	})
}
